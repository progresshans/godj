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
- GDJ-0018 hosted evidence commit / GDJ-0019 activation baseline:
  `3269d662a8b403b5d73096c04abf9fa630b22974`
  (`docs: record hosted lifecycle validation`)
- GDJ-0019 activation commit:
  `058bc0aba66c78e344f2d8bc87afa2995b2b585a`
  (`docs: activate migration definition source contracts`)
- GDJ-0019 machine/conformance commit:
  `4c7b8390c34ce4f9c4bd9524f22779208cff0df0`
  (`test: lock migration definition source contracts`)
- GDJ-0019 feasibility/final code commit:
  `58c66fdc751867a3c2f1541a8594c6615c9fbb59`
  (`test: prove migration definition loading boundary`)
- remote: `https://github.com/progresshans/godj.git`
- Draft PR: [#1 Add revision-fenced migration lifecycle](https://github.com/progresshans/godj/pull/1)
- 현재 단계: GDJ-0019 완료; active/ready work 없음
- 최근 완료 작업:
  [GDJ-0019 Migration Definition Source Compatibility Contracts](../../work/0019-migration-definition-source-compatibility-contracts.md)
- 활성 작업: 없음
- ready 작업: 없음
- 다음 planned 제품 작업: GDJ-0020 migration definition loader product slice. 아직 work item이나
  allowed paths가 없으므로 별도 activation 전에는 active/ready가 아니며 구현을 시작하지 않음

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

- Protocol v2에는 10 ordered reference set, 105 unique contract/scenario와 90 ordered
  cross-binding이 있습니다. 앞선 9개 set은 GoDj product adapter를 가지며 97 contract의 제품
  분류는 `92 passing + 5 deviation`입니다. MIG-018/020/022/024는
  [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리),
  MIG-052는
  [DEV-0002](../DEVIATIONS.md#dev-0002--app-zero의-incomparable-sibling은-godj-canonical-order를-유지)입니다.
- Tenth set MIG-057..064는 Django result parity가 아닌 Accepted
  [ADR-0019](../adr/0019-versioned-migration-definition-source.md)의 synthetic GoDj decision oracle입니다.
  8개 contract는 `oracle_locked`이며 product runner/adapter나 public loader 구현이 아닙니다.
- Source contract artifact pins는 manifest 5,195 bytes,
  `8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`; oracle 29,851 bytes,
  `efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`; static fixture 1,574 bytes,
  `41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`; `SHA256SUMS` 959 bytes,
  `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다.
- Reference registry/test pins는 scenario source 102,128 bytes,
  `53c52e3dbcd8af13e0307e62738383a01d6f307464332942c5c8ad97b71aad77`; scenario test 68,504 bytes,
  `b30b5ed338da16388fc354ecc3cdceef7d8ca8948bc41b46e4f840a0e845605a`입니다.
- Static comparison은 exit 1과 MIG-057..064 ordered mismatch 정확히 8개를 유지합니다. Product
  `godjcheck`는 이 set을 지원하지 않아 exit 2, stdout 0 bytes, actual artifact 없음으로
  fail-closed합니다.
- 기존 9 product set, 97 product contract, prior artifact byte pins와
  `92 passing + 5 deviation`은 변경되지 않았습니다.

### 검증 증거

- GDJ-0019 local/reference 증거는
  [EVID-20260809-019](TEST_EVIDENCE.md#evid-20260809-019--gdj-0019-migration-definition-source-compatibility-contracts)에
  기록했습니다.
- Final code commit에서 root `make check`, full
  `CGO_ENABLED=0 go test -count=1 ./...`, definitionload normal/race/CGO-disabled/vet/count-20이
  통과했습니다.
- Portable Python은 164 tests 중 exact-only 15 skipped, exact profile은 164/164 passing입니다.
  Source/operation/IR canonical error matrix는 Go/Python 59/59 exact parity입니다.
- 이전 GDJ-0018 checkout의 GitHub-hosted Ubuntu/macOS 증거는
  [EVID-20260809-018](TEST_EVIDENCE.md#evid-20260809-018--gdj-0018-github-hosted-ubuntu와-darwinarm64-ci)에
  남아 있습니다. GDJ-0019 final code commit `58c66fdc751867a3c2f1541a8594c6615c9fbb59`의 hosted CI는
  아직 실행하지 않았으며 pending/uncollected입니다. 이전 run을 final-head PASS로 재사용하지 않습니다.

## 확정된 결정

- [ADR-0017](../adr/0017-revision-fenced-migration-lifecycle.md)의 per-step first-write fence와
  atomic successor 안전성 방향을 [ADR-0018](../adr/0018-revision-fenced-migration-lifecycle-product-shape.md)의
  Executor-owned public product shape로 구현합니다.
- Lifecycle은 already-loaded, version-compatible `[]Migration`을 입력으로 받습니다. Source
  document/version handshake와 operation codec는 Accepted
  [ADR-0019](../adr/0019-versioned-migration-definition-source.md)이 contract-only로 정의하며,
  제품 loader/CLI는 별도 GDJ-0020 이후 범위입니다.
- Existing backend port를 widen하지 않고 optional fenced port를 추가합니다. Unsupported backend는
  fail-closed하고 legacy fallback이나 conflict 자동 retry를 제공하지 않습니다.
- Migration별 commit과 last durable state는 ADR-0014를 유지합니다. Lifecycle 전체 outer
  transaction, distributed lock, lease와 crash reconciliation은 제공하지 않습니다.
- Fresh-only bootstrap과 no-op 무생성 경계를 유지합니다. Existing database adoption/repair,
  database copy/restore epoch 정책과 overflow recovery는 후속 범위입니다.
- ADR-0013 canonical ascending planner order를 유지합니다. MIG-052의 six-path DEV-0002 외 final
  state/DB/history/phase 차이는 허용하지 않습니다.

## Accepted source contract 경계

- [ADR-0019](../adr/0019-versioned-migration-definition-source.md)은 caller-provided strict data-only
  JSON v1과 tuple `(definition format 1, loader ABI 1, operation codec 1, Schema IR 2)`을 채택합니다.
- Codec v1은 fully normalized IR v2 `CreateModel`과 non-PK `char`/`boolean` `AddField`만 허용합니다.
  Python/Go executable source, custom operation/callback/raw SQL와 file/module discovery는 비목표입니다.
- SourceID는 non-empty/unique diagnostic precedence handle이며 migration identity나 digest input이
  아닙니다. Semantic definition-set digest는 trust/revision/history fence가 아닙니다.
- MIG-057..064는 atomic batch, canonical digest/error와 existing `Executor.Migrate` reference
  handoff를 synthetic decision oracle로 잠급니다. `conformance/definitionload/**`의 proof는
  test-only이며 product package가 import하거나 public API를 제공하지 않습니다.
- 이 source tuple은 Q-010의 global CLI/library/generator semver handshake 전체를 해결하지
  않습니다. CLI는 product loader 뒤 별도 orchestration으로 다룹니다.

## 현재 차단 요인과 알려진 제한

외부 blocker는 없습니다. Hosted CI는 GDJ-0019 final code head에서 아직 실행하지 않은 별도
pending evidence입니다. 다음은 구현하지 않은 제품 범위입니다.

- Accepted migration definition source contract의 product versioned loader/API, numeric resource
  limits, structured public error와 source discovery/public CLI는 미구현
- Codec v2+, writer/upgrade/cache, executable/custom/data operation과 filesystem/module/remote adapter
- Existing database adoption/repair와 unknown commit reconciliation
- PostgreSQL/MySQL 등 non-SQLite fenced backend, multi-DB router와 distributed coordination
- Live schema drift, non-cooperating direct SQL writer, pre-cutover completed ABA와 crash repair

## 다음 정확한 작업

현재 active/ready work는 없습니다. 다음 통합 담당자는 GDJ-0020 migration definition loader product
slice의 work item, goal/non-goal, allowed paths와 completion gates를 별도로 작성하고 activation해야
합니다. 그 전에는 `migrations/**`, product runner, source adapters, writer나 CLI를 변경하지 않습니다.
Hosted CI가 필요하면 같은 PR #1에 final-head commit을 올린 뒤 새 run으로 수집하고 EVID-019 또는
후속 evidence에 실제 URL/head/result를 기록합니다.

## 작업 재개 체크포인트

- 현재 final code 기준: branch
  `codex/revision-fenced-migration-lifecycle@58c66fdc751867a3c2f1541a8594c6615c9fbb59`
- 최근 완료 work:
  [GDJ-0019](../../work/0019-migration-definition-source-compatibility-contracts.md)
- active work: 없음
- ready work: 없음
- planned next: GDJ-0020 product loader slice; 별도 activation 필요
- 현재 분류: 10 reference set/105 contract/90 ordered cross-binding; 9 product adapter/97 product
  contract, `92 passing + 5 deviation + 8 oracle_locked`
- 전체 local gate: `make check`
- Portable CI equivalent: `make ci`
- Hosted CI: PR #1 run 31295886061은 GDJ-0018 head PASS; GDJ-0019 final head는 not run/pending
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 가장 위험한 과장: Accepted JSON/source contract나 MIG-064 oracle/test-only proof를 product loader/Go
  handoff/file discovery/CLI/adoption/crash recovery 또는 non-SQLite 지원으로 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
