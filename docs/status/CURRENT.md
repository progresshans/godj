# 현재 상태

- 마지막 갱신: 2026-08-09
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `codex/revision-fenced-migration-lifecycle`
- GDJ-0018 제품 commit:
  `d076bd20f5964074b7b76b44147ca59f7b3e6eb8`
  (`feat: add revision-fenced migration lifecycle`)
- GDJ-0018 machine/conformance commit:
  `fd49d5147beefead640f43ae6fd5c83860a17a06`
  (`test: verify migration lifecycle conformance`)
- CI workflow commit:
  `7df6e2ad97d5890610e597277653df0674e8dd52`
  (`ci: validate exact darwin migration profile`)
- 반복 테스트 격리 commit:
  `9f51ad0da443d259940d44acbb8c3d095a9a257b`
  (`test: isolate repeated SQLite query classification`)
- GDJ-0018 완료 문서 commit:
  `999e63b42e6ebd89e6f0f5f531a53a9cd2ffd2f3`
  (`docs: complete revision-fenced migration lifecycle`)
- remote: `https://github.com/progresshans/godj.git`
- Draft PR: [#1 Add revision-fenced migration lifecycle](https://github.com/progresshans/godj/pull/1)
- 현재 단계: GDJ-0018 Revision-Fenced Migration Lifecycle Product Slice completed
- 최근 완료 작업:
  [GDJ-0018 Revision-Fenced Migration Lifecycle Product Slice](../../work/0018-revision-fenced-migration-lifecycle-product-slice.md)
- 활성 작업: 없음
- 다음 planned 작업: GDJ-0019 migration definition source/versioned-loader compatibility
  contracts; 아직 activation하지 않음

## 현재 checkout에서 확인된 사실

### 제품 구현

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2, deterministic codegen, generic Manager/QuerySet,
  typed/dynamic Query AST, SQLite query/write와 Save lifecycle 제품 단면이 구현됐습니다.
- Migration core는 versioned `ProjectState`, immutable graph/`AppliedState`, zero-I/O `Planner`,
  preflighted `ExecutePlan`, recorder-backed restart planning과 immutable historical-state
  reconstruction을 제공합니다.
- Accepted [ADR-0018](../adr/0018-revision-fenced-migration-lifecycle-product-shape.md)에 따라
  zero value가 invalid인 `LifecycleRequest`, `LatestLifecycleRequest`,
  `TargetedLifecycleRequest`와
  `Executor.Migrate(ctx, definitions, request)`가 구현됐습니다. Caller-owned definition,
  operation/nested IR와 target은 lifecycle 시작 전에 snapshot/deep-copy됩니다.
- `Migrate`는 Executor-owned backend의 optional `RevisionFencedBackend`만 사용합니다. 정확히 한
  atomic applied-history snapshot, explicit known-history check, state reconstruction, plan,
  full preflight와 migration별 fenced execution을 한 public lifecycle로 묶습니다. Optional port가
  없으면 legacy transaction으로 fallback하지 않습니다.
- Backend port는 connection-free `RevisionFencedSession`, declared `HistoryTransition`, dedicated
  `RevisionFencedTransaction`과 `CommitRolledBack`/`CommitCommitted`/`CommitUnknown` durability를
  사용합니다. Mandatory `Close`는 caller cancellation과 분리된 bounded cleanup context를
  사용합니다.
- `CommitCommitted`만 core의 returned state와 session token을 successor로 전진시킵니다.
  `CommitRolledBack`은 pre-step state/token을 보존하고, unknown/zero outcome은 마지막 confirmed
  pre-step state와 `commit_outcome_unknown`을 반환합니다. SQLite 구현은 rollback을 포함한 어느
  failed step 뒤에도 session을 poison하여 같은 lifecycle에서 retry하거나 tail을 다시 열지
  못하게 합니다.
- SQLite는 exact `godj_migration_revision` singleton table에 format v1, 16-byte CSPRNG epoch,
  non-negative signed int64 revision과 sorted full recorder history의 length-prefixed SHA-256
  fingerprint를 저장합니다. 각 step은 새 pinned `*sql.Conn`의 literal `BEGIN IMMEDIATE`에서
  expected token을 첫 DDL 전에 검사하고 schema, exact-one recorder transition과 successor token을
  원자적으로 commit합니다.
- Metadata와 recorder가 모두 absent인 fresh database의 첫 nonempty apply만 transaction 안에서
  bootstrap합니다. Empty plan은 metadata/recorder를 만들지 않습니다. Metadata 없이 recorder가
  존재하면 row가 0개여도 `revision_fence_adoption_required`이고 public adoption API는 없습니다.
  Metadata 생성 뒤 legacy `BeginMigration`은 fail-closed합니다.
- SQLite default-bearing `AddField`는 table이 empty일 때만 logical default를 보존하면서 physical
  persistent default 없이 허용합니다. Nonempty table은 backfill/rebuild가 없으므로 기존
  `unsupported_operation`으로 거부합니다.

### 오류와 durability 경계

- Public taxonomy에는 `migration_conflict_error`와 다음 lifecycle code가 구현됐습니다:
  `revision_fence_unsupported`, `revision_fence_adoption_required`,
  `stale_history_revision`, `history_revision_contended`, `history_revision_integrity`,
  `commit_outcome_unknown`, `commit_cleanup_failed`, `session_close_failed`.
- Backend는 source-compatible raw `RevisionFenceError`와
  adoption-required/stale/contended/integrity kind만 core에 전달합니다. Core가 public category/code로
  정규화하며 malformed typed nil이나 unknown kind는 integrity로 fail-closed합니다.
- Known dependency inconsistency와 operation/recorder/begin/rollback taxonomy는 기존 의미를
  유지합니다. Recorder stage의 generic capability error도 기존 `record_failed` 의미를 유지하고
  revision-fence raw carrier만 lifecycle taxonomy로 변환합니다.
- Primary operation/recorder/commit error가 cleanup보다 우선합니다. Committed+cleanup error는
  post-step state + `commit_cleanup_failed`, primary 없는 session close error는 last confirmed state +
  `session_close_failed`입니다. Conflict/contention/integrity/capability 어느 경우에도 semantic
  retry는 0입니다.

### 호환 계약과 machine artifact

- Protocol v2에는 9 ordered reference/product adapter set과 97 contract가 있습니다: M1
  read/metadata 11개, write/migration 11개, Save lifecycle 12개, QuerySet evaluation/cache 11개,
  migration planning 12개, plan execution 10개, recorder restart 10개, historical-state
  reconstruction 10개, migration lifecycle 10개입니다.
- 현재 제품 분류는 `92 passing + 5 deviation`입니다. MIG-018/020/022/024는
  [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리),
  MIG-052만
  [DEV-0002](../DEVIATIONS.md#dev-0002--app-zero의-incomparable-sibling은-godj-canonical-order를-유지)입니다.
- MIG-047..056 lifecycle set은 public `Executor.Migrate`와 live SQLite backend를 사용하는 product
  adapter입니다. MIG-047/048/049/050/051/053/054/055/056은 Django oracle과 exact하고,
  MIG-052만 reviewed sparse deviation입니다.
- Django MIG-052 plan은 B1←A3←A2←A1, GoDj canonical plan은 A3←A2←B1←A1입니다. B1/A3은
  incomparable sibling이며 deviation은 정확히 `result.plan[0..2]`와
  `metrics.steps[0..2]` 여섯 replace만 허용합니다. Resulting state, managed DB schema, recorder
  history와 phase는 reference와 동일합니다.
- Lifecycle manifest는 13,735 bytes, SHA-256
  `5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`입니다. DEV-0002 sparse
  fixture는 6,769 bytes, SHA-256
  `58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`입니다.
- Locked Django lifecycle oracle은 98,436 bytes, SHA-256
  `7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, static fixture는
  1,681 bytes, SHA-256
  `b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`입니다.
  `SHA256SUMS`는 SHA-256
  `520db274a63ed9d192e6ae0a3db224154a84676462e7fd8e49f80f64673c1a90`로 불변입니다.
- 두 독립 Go actual은 각각 98,304 bytes, SHA-256
  `a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical했고,
  9 exact + MIG-052 reviewed expectation에서 10 contract/0-diff였습니다. Static comparison은
  의도한 exit 1과 ordered mismatch 10개를 유지합니다.
- 기존 locked lifecycle oracle/static/SHA256SUMS와 `conformance/lifecyclefence/**`는 변경하지
  않았습니다.

### 검증 증거

- GDJ-0018의 제품, SQLite fence, live adapter, artifact와 반복/concurrency gate는
  [EVID-20260809-017](TEST_EVIDENCE.md#evid-20260809-017--gdj-0018-revision-fenced-migration-lifecycle-product-slice)에
  기록했습니다.
- PR #1의 GitHub-hosted Ubuntu/macOS 검증은
  [EVID-20260809-018](TEST_EVIDENCE.md#evid-20260809-018--gdj-0018-github-hosted-ubuntu와-darwinarm64-ci)에
  기록했습니다.
- Final tree에서 `make check`, full `CGO_ENABLED=0 go test -count=1 ./...`, migrations
  `-count=50 -shuffle=on`, db/sqlite `-count=20 -shuffle=on`, focused race 반복과 독립 P0–P3
  감사가 통과했습니다.
- Portable Python은 130 tests 중 exact-only 13 skipped, exact profile은 130/130 passing입니다.
- GitHub Actions [run 31295886061](https://github.com/progresshans/godj/actions/runs/31295886061)에서
  Ubuntu 24.04 full portable job과 macOS 15 arm64 exact lifecycle job이 모두 통과했습니다.

## 확정된 결정

- [ADR-0017](../adr/0017-revision-fenced-migration-lifecycle.md)의 per-step first-write fence와
  atomic successor 안전성 방향을 [ADR-0018](../adr/0018-revision-fenced-migration-lifecycle-product-shape.md)의
  Executor-owned public product shape로 구현합니다.
- Lifecycle은 already-loaded, version-compatible `[]Migration`을 입력으로 받습니다. Source file,
  loader/version handshake, operation codec와 CLI는 GDJ-0019 이후 범위입니다.
- Existing backend port를 widen하지 않고 optional fenced port를 추가합니다. Unsupported backend는
  fail-closed하고 legacy fallback이나 conflict 자동 retry를 제공하지 않습니다.
- Migration별 commit과 last durable state는 ADR-0014를 유지합니다. Lifecycle 전체 outer
  transaction, distributed lock, lease와 crash reconciliation은 제공하지 않습니다.
- Fresh-only bootstrap과 no-op 무생성 경계를 유지합니다. Existing database adoption/repair,
  database copy/restore epoch 정책과 overflow recovery는 후속 범위입니다.
- ADR-0013 canonical ascending planner order를 유지합니다. MIG-052의 six-path DEV-0002 외 final
  state/DB/history/phase 차이는 허용하지 않습니다.

## 현재 차단 요인과 알려진 제한

외부 blocker와 미실행된 현재 CI gate는 없습니다. 다음은 구현하지 않은 제품 범위입니다.

- Migration definition source discovery/versioned loader/codec와 public CLI
- Existing database adoption/repair와 unknown commit reconciliation
- PostgreSQL/MySQL 등 non-SQLite fenced backend, multi-DB router와 distributed coordination
- Live schema drift, non-cooperating direct SQL writer, pre-cutover completed ABA와 crash repair

## 다음 정확한 작업

GDJ-0019를 별도 work/ADR activation으로 시작해 migration definition source와 versioned-loader
compatibility contract를 먼저 고정합니다. File discovery, format version/codec negotiation,
deterministic load order, duplicate/partial source failure와 already-loaded `Executor.Migrate` handoff를
분리해 설계합니다. GDJ-0019는 planned 상태이며 현재 active work는 아닙니다.

## 작업 재개 체크포인트

- 현재 제품 기준: branch
  `codex/revision-fenced-migration-lifecycle@9f51ad0da443d259940d44acbb8c3d095a9a257b`
- 최근 완료 work:
  [GDJ-0018](../../work/0018-revision-fenced-migration-lifecycle-product-slice.md)
- active work: 없음
- planned next: GDJ-0019 source/versioned-loader compatibility contracts
- 현재 분류: 9 product adapter set, 97 contract, `92 passing + 5 deviation`
- 전체 local gate: `make check`
- Portable CI equivalent: `make ci`
- Hosted CI: PR #1 run 31295886061의 Ubuntu 24.04와 macOS 15 arm64 job PASS
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 가장 위험한 과장: loaded-definition lifecycle을 file loader/CLI/adoption/crash recovery 또는
  non-SQLite 지원으로 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
