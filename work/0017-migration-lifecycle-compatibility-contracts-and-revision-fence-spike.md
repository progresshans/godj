---
id: GDJ-0017
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "9856fd0278162af0a5ee28dfebd4f07d93eca790"
depends_on: ["GDJ-0016"]
contracts: ["MIG-047..MIG-056", "Q-012"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "NOTICE.md", "conformance/README.md", "conformance/contracts/migration-lifecycle-manifest.json", "conformance/runners/django/runner.py", "conformance/runners/django/migration_lifecycle_scenarios.py", "conformance/runners/django/migration_lifecycle_fixture/**", "conformance/runners/django/tests/test_migration_lifecycle_scenarios.py", "conformance/runners/django/tests/test_runner_safety.py", "conformance/runners/django/tests/test_scenarios.py", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS", "conformance/fixtures/godj-migration-lifecycle-not-implemented.json", "conformance/internal/protocol/migration_lifecycle_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/lifecyclefence/**", "docs/ARCHITECTURE.md", "docs/COMPATIBILITY.md", "docs/LICENSING.md", "docs/OPEN_QUESTIONS.md", "docs/ROADMAP.md", "docs/TESTING.md", "docs/adr/0017-revision-fenced-migration-lifecycle.md", "docs/adr/README.md", "docs/status/CURRENT.md", "docs/status/IMPLEMENTATION_MATRIX.md", "docs/status/TEST_EVIDENCE.md", "work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md", "work/README.md"]
integration_owner: "one primary agent"
---

# Migration Lifecycle Compatibility Contracts and Revision-Fence Spike

## 사용자에게 보이는 결과

Durable recorder snapshot에서 목표를 정하고 historical `ProjectState`를 재구성해 plan을
실행하는 전체 migration lifecycle의 외부 의미를 Django 6.1 exact reference로 고정합니다.
동시에 두 실행자가 같은 snapshot에서 출발해도 stale 실행이 schema/recorder를 변경하지
않도록 하는 revision fence가 현재 migration별 commit 의미를 보존할 수 있는지 격리된
feasibility spike로 검증합니다.

이번 작업은 제품 lifecycle API를 제공하지 않습니다. 완료 상태는 기존 여덟 제품 set의
`83 passing + 4 deviation`과 새 열 계약의 `10 oracle_locked`를 구분합니다.

## 목표

- MIG-047..056 exact Django/SQLite disposable lifecycle probe
- Fresh/applied-prefix/fully-applied에서 latest target까지의 end-to-end 의미 고정
- Named forward/reverse와 app zero target의 desired-state 의미 고정
- Unknown legacy record 보존과 known inconsistent history의 pre-write 거부 고정
- 중간 forward 실패의 durable prefix, failed-step rollback, tail stop과 fresh restart 고정
- Ninth manifest/oracle/static fixture, provenance와 checksum 구축
- Nine-set global identity/scenario uniqueness와 72 ordered cross-binding 거부
- 기존 `83 passing + 4 deviation` 제품 결과와 여덟 locked artifact set 보존
- `conformance/lifecyclefence/**`에서 opaque revision snapshot과 migration별 transactional
  validation의 동시 실행 가능성 검증
- [Proposed ADR-0017](../docs/adr/0017-revision-fenced-migration-lifecycle.md)을 spike
  결과에 따라 Accepted, 수정 또는 Rejected할 근거 확보

## 비목표

- `migrations/**`, `migrations/backend/**`, `db/**` 제품 source 변경
- `conformance/runners/godj/**` 제품 adapter 또는 public lifecycle 구현
- 기존 `Executor`, `Planner`, `StateReconstructor`, recorder reader public shape 변경
- Public type/function 이름, zero value와 final error taxonomy 확정
- Migration definition file encoding, source/module/directory loader와 operation codec
- Public `godj migrate`, `showmigrations`, `makemigrations` 또는 project-binary handshake
- Data migration callback/plugin ABI와 historical app registry
- Replacement/squash/merge/fake/fake-initial, optimizer와 conflict resolution
- Schema drift/crash repair, live schema reconciliation와 kill-safe recovery
- Long-lived lease, fairness, distributed consensus 또는 자동 retry
- PostgreSQL, multi-DB router와 non-SQLite backend 제품 지원
- 기존 여덟 manifest/oracle/static/deviation payload의 변경

## 선행 조건과 기준 상태

- 활성 작업 baseline은 `main@9856fd0278162af0a5ee28dfebd4f07d93eca790`
  (`docs: complete historical state reconstruction`)입니다.
- 제품 구현 baseline은 `main@3b0e68d6717a9612debc9cb93d03ab0f98005860`입니다.
- [GDJ-0016](0016-historical-project-state-reconstruction-product-slice.md)은 recorder-backed
  `AppliedState`에서 immutable historical `ProjectState`를 pure replay하고 MIG-037..046을
  `passing`으로 전환했습니다.
- [ADR-0014](../docs/adr/0014-migration-plan-execution-atomic-reverse.md)는 outer
  transaction이 아닌 migration별 commit과 first-failure last durable state를 채택했습니다.
- [ADR-0015](../docs/adr/0015-recorder-backed-applied-state.md)의 recorder read는 한 시점의
  snapshot일 뿐, 이후 plan/execution과 원자적으로 결속되지 않습니다.
- 현재 `LoadAppliedState → Reconstruct → Plan → ExecutePlan` 사이에는 다른 writer가
  recorder를 변경해도 stale plan이 실행될 수 있는 TOCTOU gap이 있습니다.
- Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  CPython 3.14.3, SQLite 3.50.4, UTC/C locale입니다.
- 기존 분류는 8 set, 87 contract, `83 passing + 4 deviation`, 56 ordered
  cross-binding입니다. Working tree의 범위 밖 사용자 변경은 보존합니다.

## Django Reference / Contract

우선 확인할 pinned provenance는 다음입니다.

- `django/db/migrations/executor.py`: `MigrationExecutor.migration_plan()`,
  `_create_project_state()`, `migrate()`, `apply_migration()`, `unapply_migration()`
- `django/db/migrations/loader.py`: applied history consistency와 graph loading
- `django/db/migrations/recorder.py`: durable applied/unapplied identity
- `django/db/migrations/migration.py`: `apply()`, `unapply()`, state mutation
- `tests/migrations/test_executor.py`: forward/backward target, failure와 restart behavior
- `tests/migrations/test_loader.py`: inconsistent applied history

Contract phase는 다음처럼 고정합니다.

| ID | Phase | 잠글 외부 동작 |
|---|---|---|
| MIG-047 | `commit` | Fresh database에서 latest target으로 실행하면 full forward order가 commit되고 schema, recorder와 resulting state가 latest에 도달 |
| MIG-048 | `commit` | Durable applied prefix에서 latest target으로 실행하면 이미 applied step은 반복하지 않고 remaining tail만 commit |
| MIG-049 | `commit` | Latest target까지 이미 applied이면 lifecycle은 no-op이며 schema/recorder와 transaction count가 변하지 않음 |
| MIG-050 | `commit` | Named forward target은 dependency closure와 target까지만 commit하고 descendant는 실행하지 않음 |
| MIG-051 | `commit` | Earlier named target으로 되돌리면 target의 applied descendant만 reverse하고 named target과 unrelated applied branch는 보존 |
| MIG-052 | `commit` | App zero target `(app_label, None)`은 그 app의 applied migration과 이를 의존하는 cross-app migration을 dependent-first로 reverse해 selected app을 zero state로 이동 |
| MIG-053 | `commit` | Graph에 없는 unknown legacy recorder identity는 보존하면서 known applied prefix에서 known latest tail을 정상 commit |
| MIG-054 | `evaluation` | Public command orchestration이 `loader.check_consistent_history(connection)`를 target/plan/migrate보다 먼저 호출해 known inconsistent history를 fail-closed |
| MIG-055 | `rollback` | Forward middle A2 실패 시 A1은 durable하고 A2 schema/record는 rollback되며 tail A3/B1은 시작하지 않음 |
| MIG-056 | `commit` | MIG-055의 durable temporary file database를 닫고 fresh connection/loader/executor로 다시 열면 A1은 반복하지 않고 resume plan A2/A3/B1을 실행해 latest에 도달 |

MIG-047..053과 MIG-056은 `commit`, MIG-054는 `evaluation`, MIG-055는 `rollback`입니다.
MIG-051의 named target은 desired applied endpoint이므로 target 자체는 유지하고 descendant만
reverse합니다. MIG-052의 zero target은 empty target list나 omitted latest와 같지 않으며,
selected app migration을 직접 또는 간접 의존하는 cross-app descendant도 함께 reverse합니다.

## Observation payload과 비교 경계

각 scenario는 독립 disposable file-backed SQLite database와 fresh loader/executor를 사용하고
최소한 다음을 관찰합니다.

- Requested target mode: latest, named migration 또는 app zero target
- Capture 전/후 sorted applied identities; unknown legacy identity 포함
- Canonical logical `ProjectState`와 managed schema/row inventory
- Ordered compact lifecycle steps: app/name/direction/outcome
- Forward lifecycle의 abstract commit/rollback outcome, recorder membership과 unstarted-tail fact
- Structured error category/code와 failure step
- Fresh restart 여부와 이전 durable prefix 보존

Fresh database의 recorder table bootstrap은 active migration step과 별도인 lifecycle setup
transaction입니다. Payload에 포함할 때는 `recorder_bootstrap: created|existing` 같은 독립
fact로 정규화하고 migration step의 commit/rollback count에 섞지 않습니다.

SQL 문자열, exact SELECT 횟수, Python private graph/loader/cache/object identity,
`django_migrations.applied` timestamp와 incomparable sibling의 private traversal order는 비교하지
않습니다. Physical DDL/write count와 SQL 원문은 contract가 아니며 lifecycle step과 최종
schema·recorder side effect로 비교합니다. MIG-054 preflight의 mutation 0과 MIG-055 failed/tail
unstarted는 별도 sentinel로 검증합니다. 특히 MIG-051/052 backward는 physical reverse transaction topology와
`schema_then_record` 순서를 payload에 넣지 않고 abstract ordered step outcome과 final
schema/recorder만 잠급니다. 그 차이는 기존 DEV-0001/ADR-0014가 소유하며 이 contract에서 새
deviation을 만들지 않습니다.

Contract별 comparison dimension은 성공 contract의 result/DB state/metrics, MIG-054/055의
error/DB state/metrics를 최소로 포함합니다. Protocol observation은 result와 error를 동시에
갖지 않습니다. MIG-055의 last durable state는 DB state와 compact step metrics로
관찰합니다. Disposable probe review에서 의미를 관찰할 수 없는 필드는 억지로 protocol에
추가하지 않습니다.

## False-green 위험과 필수 gate

- **Precomputed plan/result**: recorder prefix, target 또는 migration definition을 바꾸면
  derived steps, resulting state와 schema/recorder가 함께 달라져야 합니다.
- **Private executor shortcut**: runner는
  `loader.check_consistent_history(connection) -> migration_plan -> executor.migrate` public
  orchestration만 호출합니다. `_create_project_state`, `_migrate_all_*`, `apply_migration`,
  `unapply_migration` 직접 호출은 금지하고 fixture loader의 `load_disk` seam은 source loader를
  계약하지 않는 in-memory definition injection에만 사용합니다.
- **Setup instance 재사용**: capture마다 fresh loader/executor/connection을 만들고 MIG-056은
  MIG-055의 durable file만 넘기며 in-memory plan/state/cache는 넘기지 않습니다.
- **Already-applied 재실행**: MIG-048/049는 operation과 transaction count로 duplicate
  execution을 검출합니다.
- **Target 의미 혼합**: omitted latest, named target, app zero target을 서로 다른 scenario와
  request fact로 유지합니다.
- **Unknown record 손실**: MIG-053의 unknown identity는 result와 durable recorder 모두에
  남아야 하며 known state를 materialize하거나 known tail을 막아서는 안 됩니다.
- **Preflight 위장**: MIG-054는 public command orchestration이 명시적으로
  `loader.check_consistent_history(connection) → target/plan → migrate` 순서를 소유합니다.
  `plan_invoked=false`, transaction/DDL/recorder write 0과 managed-schema before/after 불변을
  함께 확인하며 `MigrationExecutor.migrate()` 자체의 암묵적 preflight로 표현하지 않습니다.
- **실패 뒤 false completion**: MIG-055는 A1 durable, A2 rollback, A3/B1 unstarted를
  개별 sentinel로 확인합니다. MIG-056은 `:memory:` 또는 같은 in-memory connection을 재사용하지
  않고 temporary file database를 close/reopen한 fresh connection/loader/executor가 resume
  plan A2/A3/B1을 실행하고 A1을 반복하지 않는지 확인합니다. 이를 process
  restart로 표현하지 않습니다.
- **Comparator omission**: applied identity, target, step direction/order/outcome, state,
  schema/records, error와 transaction metrics mutation이 각각 mismatch를 내야 합니다.
- **Hash/map order**: 두 random-hashseed process의 canonical oracle bytes가 다르면 잠그지
  않습니다.
- **False product support**: GoDj product runner는 MIG-047..056을 exit 2/no actual로
  fail-closed하고 `godj-conformance`는 여덟 adapter만 유지합니다.
- **Cross-set 결속 누락**: 9 set의 ID/scenario 전역 uniqueness와 36 set pair의 양방향
  72 ordered cross-binding을 모두 거부합니다.

## Revision-Fence Feasibility Spike

Exact Django contract와 별개로 `conformance/lifecyclefence/**`에서만 다음 가설을
검증합니다. 이 spike는 제품 source나 public API가 아닙니다.

```text
atomic history snapshot = sorted recorder identities + opaque revision
→ pure reconstruct/check/plan
→ for each migration step:
   begin step transaction
   validate expected revision inside the transaction before first DDL/write
   apply schema + recorder mutation
   commit one migration and obtain/bind the successor revision
→ next step
```

- Revision은 순서나 시간 의미가 없는 opaque equality token이고 unknown recorder row를
  포함한 전체 history snapshot에 결속됩니다.
- 각 migration transaction은 첫 DDL/recorder write 전에 expected revision을 검증합니다.
  성공 step 이후 다음 expected revision을 안전하게 결속하는 방식은 spike에서 입증하며,
  검증과 successor handoff 사이에 stale-acceptance window를 허용하지 않습니다.
- Stale token은 current step의 schema/recorder mutation 0과 structured conflict를 반환하고,
  caller에게는 마지막으로 commit된 `ProjectState`만 남깁니다.
- 하나의 outer transaction을 사용하지 않고 ADR-0014의 migration별 durable commit을
  보존합니다.
- Conflict를 자동 retry하지 않습니다. 재-read/reconstruct/replan은 caller의 명시적 새
  lifecycle 시도입니다.
- Fence capability가 없는 backend는 unfenced execution으로 조용히 fallback하지 않고
  structured unsupported/capability error로 fail-closed합니다.

Spike gate는 적어도 다음을 포함합니다.

1. Snapshot 뒤 first step 전에 competing recorder commit이 일어나면 DDL/recorder mutation 0
2. Step 사이 competing commit이면 이전 own step만 durable하고 current/tail은 시작하지 않음
3. 같은 revision으로 동시에 시작한 두 contender 중 최대 하나만 해당 step을 commit하며
   duplicate recorder/schema mutation 없음
4. Cancellation, validation/error와 rollback 뒤 transaction/connection resource가 해제되고
   fresh 시도가 성공
5. 두 독립 connection/process와 반복 실행에서 같은 safety invariant 유지

이 spike는 live schema drift를 revision으로 감지한다고 주장하지 않습니다. Recorder history
밖의 schema mutation, crash reconciliation, fairness와 lease는 별도 범위입니다.

## 설계와 가설

[Proposed ADR-0017](../docs/adr/0017-revision-fenced-migration-lifecycle.md)의 후보는 loaded
definition, recorder snapshot, `StateReconstructor`, `Planner`, `Executor`를 한 orchestration
flow에서 사용하되 pure components와 backend transaction 경계를 유지하는 immutable
lifecycle coordinator입니다. Contract 단계에서는 public 이름과 constructor/request shape를
확정하지 않습니다.

Backend 후보 capability는 identities와 revision을 같은 snapshot에서 읽고, 각 step transaction
안에서 expected revision을 DDL 전에 검증하며, successful recorder mutation과 successor
revision을 원자적으로 결속해야 합니다. 기존 `AppliedMigrationReader`, `AtomicBackend`와
external fake의 source compatibility를 깨뜨리지 않는 별도 optional port가 우선 가설입니다.

## 구현 단계

1. Pinned Django executor/loader/recorder와 upstream tests를 MIG-047..056에 연결합니다.
2. 독립 disposable SQLite lifecycle probe와 failure/unknown/history fixtures를 작성합니다.
3. Compact step/state/schema/recorder/error normalizer와 semantic mutation gate를 먼저
   검증합니다.
4. Ninth manifest, Django registry, exact oracle, not-implemented fixture와 checksum을
   연결합니다.
5. Product unknown-scenario exit 2/no output과 product target 8-adapter 유지를 검증합니다.
6. Nine-set global uniqueness와 72 ordered cross-binding을 전부 거부합니다.
7. Two-process random-hashseed byte identity와 기존 여덟 locked payload 불변을 검증합니다.
8. `conformance/lifecyclefence/**`에서 two-connection/process revision-fence spike를
   fault/concurrency/cancellation test로 실행합니다.
9. 기존 `83 passing + 4 deviation`, full/race/CGO=0/vet와 portable/exact Python을 회귀
   검증합니다.
10. 독립 exact-contract/false-green/fence audit 뒤 ADR-0017 상태와 다음 product work
    범위를 결정합니다.

## 완료 조건

- [ ] MIG-047..056 exact disposable probe와 provenance/payload review
- [ ] Phase가 047..053/056 `commit`, 054 `evaluation`, 055 `rollback`으로 잠김
- [ ] Fresh/prefix/fully-applied/latest와 named forward/reverse/zero target 검증
- [ ] Unknown legacy preserved, inconsistent known history pre-write rejection 검증
- [ ] Middle failure durable-prefix/rollback/tail-stop와 fresh restart 검증
- [ ] Oracle two-process byte identity, checksum과 static ordered 10 status mismatch
- [ ] Product adapter 없음이 exit 2/no actual이고 product conformance가 8 set임
- [ ] Nine-set ID/scenario uniqueness와 72 ordered cross-binding 거부
- [ ] Target/history/definition/result/error/schema/recorder/metrics mutation false-green gate
- [ ] Revision fence stale-before-write, between-step conflict와 simultaneous contender gate
- [ ] Fence cancellation/error resource release와 subsequent success, race/repetition gate
- [ ] Unsupported fence capability가 structured fail-closed하고 auto retry가 없음
- [ ] Existing `83 passing + 4 deviation` 회귀 없음
- [ ] 완료 분류가 `83 passing + 4 deviation + 10 oracle_locked`, 9 set/97 contract임
- [ ] 기존 여덟 locked manifest/oracle/static/deviation payload와 checksum entry 불변
- [ ] Full Go/race/CGO=0/vet, portable/exact Python, checksum과 Markdown/link gate 통과
- [ ] ADR/work/CURRENT/matrix/evidence가 같은 checkout과 상태를 가리킴

## 진행 기록

- [x] GDJ-0016 historical state product와 `83 passing + 4 deviation` 완료
- [x] Stale recorder snapshot과 execution 사이 TOCTOU gap 분리
- [x] Contract-only exact lifecycle 범위와 revision-fence spike 경계 작성
- [x] Proposed ADR-0017 작성
- [ ] Pinned source/provenance와 exact one-off probes
- [ ] Ninth machine artifact와 false-green gates
- [ ] Revision-fence feasibility implementation/audit
- [ ] Full verification와 handoff

## 수정 파일

현재 activation 문서는 이 work와 ADR-0017, work/ADR index, CURRENT, ROADMAP,
OPEN_QUESTIONS만 변경합니다. Contract/spike 구현 후에는 front matter의 `allowed_paths` 안에서
실제 변경 파일과 역할을 여기에 기록합니다.

다음 경로는 명시적으로 금지합니다.

- `migrations/**`, `db/**`: 제품 lifecycle/backend 변경 금지
- `conformance/runners/godj/**`: 새 product adapter 금지
- 기존 8개 contract manifest, Django oracle와 static/deviation fixture: locked bytes 변경 금지
- `conformance/oracles/**/SHA256SUMS`: 기존 entry 수정 금지, 새 lifecycle oracle entry 추가만 허용
- 외부 `/Users/hanhyeonjin/Documents/django` reference checkout: 수정 금지

## 결정된 사항

- 2026-08-08: Loader/CLI보다 lifecycle exact contract와 revision-fence feasibility를 먼저
  진행합니다. Existing Go `Migration` value가 definition을 표현할 수 있어 source encoding을
  먼저 freeze할 필요가 없고, stale snapshot은 현재 제품 경계에 이미 존재하는 safety gap입니다.
- 2026-08-08: Exact lifecycle behavior와 GoDj-specific concurrency fence는 같은 제품
  지원으로 합치지 않습니다. Ninth set은 `oracle_locked`, fence는 격리 spike입니다.
- 2026-08-08: Per-migration commit과 last durable state를 보존하며 outer transaction과
  automatic retry는 채택하지 않습니다.
- 2026-08-08: Fence unsupported backend는 fail-closed 후보이며 silent fallback은 금지합니다.

## 미결정/Blocker

외부 blocker는 없습니다. 다음은 spike/contract 결과 전에는 확정하지 않습니다.

- Public lifecycle coordinator와 target request type 이름/zero value
- Opaque revision storage/encoding과 successor token handoff interface
- Existing reader/backend에 대한 optional capability discovery shape
- Conflict/capability error의 final category/code와 external fake compatibility
- Same database의 schema drift를 recorder revision 밖에서 다룰 별도 recovery protocol

## 테스트 증거

- Evidence ID: activation 단계에는 없음
- Command: 문서 link/상태 검증만 실행
- Result: 구현 완료 시 실제 명령, checkout과 결과를 기록
- Not run: Django oracle, lifecyclefence spike, full product gate, hosted CI

## 위험과 rollback

- Lifecycle convenience API가 lock/crash safety를 과장할 위험이 있으므로 contract/spike
  단계에서는 product source와 public 이름을 만들지 않습니다.
- Revision validation이 transaction 밖에 있거나 successor token handoff가 분리되면 새 TOCTOU가
  생깁니다. 이 invariant를 만족하지 못하면 ADR-0017을 Accepted하지 않습니다.
- Outer transaction은 partial durable semantics를 깨뜨리므로 선택지에서 제외합니다.
- Contract artifact는 기존 bytes를 덮어쓰지 않고 새 set으로 추가합니다. 실패 시 새 lifecycle
  artifact/spike만 되돌릴 수 있어야 합니다.

## 다음 정확한 작업

Pinned Django `MigrationExecutor`/`MigrationLoader`/`MigrationRecorder`와
`tests/migrations/test_executor.py`, `test_loader.py`에서 MIG-047..056 provenance를 contract별로
확인한 뒤 disposable exact probe를 먼저 작성합니다. 동시에 제품 package를 import하지 않는
`conformance/lifecyclefence/**` test harness에서 atomic snapshot/revision과 first-write fence의
최소 interface를 test-first로 검증합니다.

## 결과와 인수인계

아직 activation 상태입니다. GDJ-0017이 완료되기 전에는 MIG-047..056을 product `passing`으로,
revision-fence spike를 public migration lifecycle 지원으로 표현하지 않습니다. 다음 제품 work는
exact oracle과 spike 감사 뒤 별도로 생성합니다.
