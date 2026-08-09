# 현재 상태

- 마지막 갱신: 2026-08-10
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
- GDJ-0019 completion/hosted-tested head:
  `4d9a64a0c42406bda931820f7eb38a0f737d117c`
  (`docs: complete migration definition source contracts`)
- GDJ-0019 hosted evidence / GDJ-0020 activation baseline:
  `eecc75f7507414ad6043a090c97b84080ab0fb8b`
  (`docs: record hosted definition source validation`)
- GDJ-0020 activation commit:
  `5942a0bedd6cca7fe93e52d90219a01193c6f534`
  (`docs: activate migration definition loader product slice`)
- GDJ-0020 product/hosted-tested commit:
  `6172d843a4bb234592cafc176a8d1191933b141c`
  (`feat: add bounded migration definition loader`)
- GDJ-0020 completion-documentation/hosted-tested commit:
  `a5422f2c1ba5db34986564fc065e4b8e28ef0115`
  (`docs: complete migration definition loader product slice`)
- GDJ-0020 hosted evidence / GDJ-0021 activation baseline:
  `53729103651bfc34acc5fe07fb4376d5dd78c204`
  (`docs: record hosted loader completion validation`)
- GDJ-0021 activation commit:
  `fbc3c7cfc2fd779117944b8e2479a6a2bf17fdb5`
  (`docs: activate project-linked migration check contracts`)
- GDJ-0021 contract/hosted-tested implementation commit:
  `84ddf109c04acd72992b816aa72140c6e748e5f0`
  (`test: lock project-linked migration check contracts`)
- GDJ-0021 completion-documentation/hosted-tested commit:
  `34ae58fc2490deb8f884a0b5591520b11bae8669`
  (`docs: complete project-linked migration check contracts`)
- remote: `https://github.com/progresshans/godj.git`
- Draft PR: [#1](https://github.com/progresshans/godj/pull/1)
- 현재 단계: GDJ-0021 project-linked migration check compatibility contracts completed; ADR-0021 Accepted
- 최근 완료 작업:
  [GDJ-0021 Migration Project Check Compatibility Contracts](../../work/0021-migration-project-check-compatibility-contracts.md)
- 활성 작업: 없음
- ready 작업: 없음
- completion 상태: GDJ-0021 local/reference/independent review는
  [EVID-20260810-024](TEST_EVIDENCE.md#evid-20260810-024--gdj-0021-project-linked-migration-check-compatibility-contracts),
  exact implementation-head 10-job CI는
  [EVID-20260810-025](TEST_EVIDENCE.md#evid-20260810-025--gdj-0021-github-hosted-10-job-implementation-head-ci)에
  기록했습니다. Exact 16-file completion-documentation head의 별도 10-job CI는
  [EVID-20260810-026](TEST_EVIDENCE.md#evid-20260810-026--gdj-0021-github-hosted-completion-documentation-head-10-job-ci)에
  기록했습니다. Draft PR #1은 open/draft/clean이고 completion commit `34ae58f`의
  [run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)은 existing 2 +
  project-check 4 + SQLite 4의 exact 10 job을 모두 통과했습니다. 현재 EVID-026 append/status 교정의
  exact 8-file patch 자체의 hosted CI는 commit/push 전 `not run/pending`입니다.

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
- 새 leaf package `migrations/definition`은 caller-provided `Source` JSON bytes를 파일 I/O 없이
  snapshot하고 tuple `(1,1,1,2)`, strict closed codec과 normalized IR을 bounded하게 검증합니다.
  `Load(...Source)`는 zero `Set`을 canonical empty set으로 정의하고 success/error 모두 value-only
  `LoadReport`를 반환하며, failure에서 partial definition을 publish하지 않습니다.
- Loader는 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, JSON depth 64,
  per-document values 65,536, aggregate values 262,144, dependencies 2,047, operations 2,048,
  CreateModel fields 2,048의 exact cap을 적용합니다. Strict scanner는 any-depth duplicate,
  surrogate/numeric lexeme와 RFC 6901 error order를 bounded lazy path representation으로 처리합니다.
- `Set` accessor는 raw source bytes를 보존하지 않고 매번 dependency/operation/nested IR까지 fresh
  deep copy합니다. `Set.Migrate`는 fresh definitions와 caller의 immutable request value를 existing
  `Executor.Migrate`에 정확히 한 번 전달하며 graph/lifecycle error를 wrap/reclassify하지 않습니다.

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

- Protocol v2에는 11 ordered reference set, 115 unique contract/scenario와 110 ordered
  cross-binding이 있습니다. 현재 local product checkout은 그중 기존 10개 set만 actual GoDj adapter를
  가지며 105 contract의 제품 분류는 `100 passing + 5 deviation`입니다. MIG-018/020/022/024는
  [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리),
  MIG-052는
  [DEV-0002](../DEVIATIONS.md#dev-0002--app-zero의-incomparable-sibling은-godj-canonical-order를-유지)입니다.
- Tenth set MIG-057..064는 Django result parity가 아닌 Accepted
  [ADR-0019](../adr/0019-versioned-migration-definition-source.md)의 synthetic GoDj decision oracle입니다.
  Manifest/adapter의 8개 contract가 `passing`이며 public loader actual로 관찰합니다. Product commit
  `6172d843a4bb234592cafc176a8d1191933b141c`의 exact-head hosted CI까지 통과해 GDJ-0020은
  completed입니다.
- Eleventh set MIG-065..074도 Django parity가 아닌 Accepted
  [ADR-0021](../adr/0021-project-linked-migration-check.md)의 independent GoDj decision oracle입니다.
  Manifest status는 exact 10 `oracle_locked`이고 product adapter가 없습니다. Test-only
  `conformance/projectcheck` proof가 descriptor/selection/build/protocol/flat no-follow discovery,
  `definition.Load` exactly once, DB/lifecycle call 0, 11 caps와 Unix cancel/reap/cleanup 의미를
  검증합니다. 전역 CLI나 public project package 구현으로 분류하지 않습니다.
- Source contract artifact pins는 status-only manifest 5,147 bytes,
  `688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488`; oracle 29,851 bytes,
  `efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`; static fixture 1,574 bytes,
  `41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`; `SHA256SUMS` 959 bytes,
  `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다.
- Reference registry/test pins는 scenario source 102,128 bytes,
  `53c52e3dbcd8af13e0307e62738383a01d6f307464332942c5c8ad97b71aad77`; status-only assertion이
  바뀐 scenario test는 68,498 bytes,
  `b8237e761caaf98ae050cc9fcb3031ead3f5fb9c40b7ce53ec2dc451012d2ecc`입니다.
- Project-check artifact pins는 manifest 4,580 bytes
  `0cd8d77b03820af75c8bda8434620f40acd1a3cb6319cf4fb732db4b38d44218`, static fixture 1,729 bytes
  `86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`, oracle 19,971 bytes
  `49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, 11-line `SHA256SUMS`
  1,061 bytes `74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`입니다. 기존 10-line/
  959-byte prefix `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`는 불변입니다.
- Existing MIG-057..064 product comparison은 locked reference oracle과 difference 0입니다. 새
  project-check static comparison은 exit 1/ordered mismatch 10, product `godjcheck`는 새 reference
  set을 conformance-tool exit 2/no actual로 fail-closed합니다. Django-derived set의 기존 성공 문구는
  `locked Django oracle`, synthetic decision set은 `locked reference oracle`로 구분합니다.
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
- GDJ-0020 product에서 `make check`, focused normal/race/CGO-disabled/vet/count-20,
  5초 scanner fuzz 150,235 executions, exact Python 164/164와 모든 conformance/oracle gate가
  통과했습니다. Linux/386 test binary cross-compile 뒤 exact product-head Ubuntu job에서 실제
  `CGO_ENABLED=0 GOARCH=386` runtime도 통과했습니다. 상세 local 결과는
  [EVID-20260809-021](TEST_EVIDENCE.md#evid-20260809-021--gdj-0020-bounded-migration-definition-loader-product-slice)에
  기록했습니다.
- 독립 `godjcheck` 두 process actual은 각각 29,631 bytes,
  `a3f40f9bbee06d4edc4af0a00f40a76da259207995ac20d030101aa2ec3aec87`로 서로 byte-identical이고
  locked oracle과 protocol difference 0입니다. Go actual JSON과 Python oracle의 raw bytes 동일성은
  계약이 아닙니다.
- Independent final code/integration review는 P0/P1/P2/P3 finding 0으로 종료했습니다.
- GDJ-0019 completion head `4d9a64a0c42406bda931820f7eb38a0f737d117c`는 Draft PR #1
  [run 31302983804](https://github.com/progresshans/godj/actions/runs/31302983804)에서 Ubuntu 24.04
  portable gate와 macOS 15 arm64 exact gate가 모두 통과했습니다. 상세 로그와 checkout 범위는
  [EVID-20260809-020](TEST_EVIDENCE.md#evid-20260809-020--gdj-0019-github-hosted-ubuntu와-darwinarm64-ci)에
  기록했습니다.
- GDJ-0020 product commit `6172d843a4bb234592cafc176a8d1191933b141c`는 Draft PR #1
  [run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)에서 Ubuntu 24.04와
  macOS 15 arm64가 모두 통과했습니다. Ubuntu는 `make ci`, 실제 Linux/386 definition test,
  checksum/no-rewrite를, macOS는 focused Go, exact Python 164/164, all-oracle/no-rewrite를
  통과했습니다. 상세 hosted 결과는
  [EVID-20260809-022](TEST_EVIDENCE.md#evid-20260809-022--gdj-0020-github-hosted-product-head-ci)에
  기록했습니다. 이 product-head run은 completion-documentation head의 CI 증거로 재사용하지 않았고,
  아래 별도 exact-head run으로 후속 상태를 검증했습니다.
- GDJ-0020 completion-documentation commit
  `a5422f2c1ba5db34986564fc065e4b8e28ef0115`는 같은 Draft PR #1
  [run 31310002784](https://github.com/progresshans/godj/actions/runs/31310002784)에서 exact head로
  다시 검증됐습니다. Ubuntu 24.04.4 job
  [93236227654](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227654)는
  `make ci`, 실제 Linux/386 definition runtime, checksum/no-rewrite를, macOS 15.7.7 arm64 job
  [93236227698](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227698)는
  focused CGO-disabled Go, exact Python 164/164, all-oracle/no-rewrite를 통과했습니다. 상세 결과는
  [EVID-20260809-023](TEST_EVIDENCE.md#evid-20260809-023--gdj-0020-github-hosted-completion-documentation-head-ci)에
  기록했습니다. 그 evidence patch commit `53729103651bfc34acc5fe07fb4376d5dd78c204` 자체도 이후
  Draft PR #1 run 31310606332의 Ubuntu/macOS 두 job을 통과했습니다.
- GDJ-0021 local/reference proof에서 project-check normal/race/CGO-disabled/vet/count-20, root
  `make ci`, exact Python 174/174, 11-oracle/checksum/no-rewrite, static exit 1/10와 product exit 2/no
  actual이 모두 통과했습니다. Independent contract/integration 및 filesystem/process/security
  audit는 각각 P0/P1/P2/P3 finding 0입니다. 상세 명령과 artifact pin은
  [EVID-20260810-024](TEST_EVIDENCE.md#evid-20260810-024--gdj-0021-project-linked-migration-check-compatibility-contracts)에
  기록했습니다.
- GDJ-0021 implementation commit `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
  [run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)의 exact 10 job에서
  통과했습니다. Existing Ubuntu 24.04.4 full/macOS 15.7.7 arm64 exact 두 job과
  `ubuntu-22.04`, `ubuntu-24.04-arm`, `macos-15-intel`, `macos-26` project-check/SQLite 각 4-leg가
  expected GOOS/GOARCH, normal/race/CGO-disabled/vet와 final clean-worktree gate를 모두 통과했습니다.
  상세 job ID/명령은
  [EVID-20260810-025](TEST_EVIDENCE.md#evid-20260810-025--gdj-0021-github-hosted-10-job-implementation-head-ci)에
  기록했습니다.
- GDJ-0021 completion-documentation commit
  `34ae58fc2490deb8f884a0b5591520b11bae8669`도 같은 Draft PR #1의 별도
  [run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)에서 exact 10 job을
  다시 통과했습니다. Ubuntu full은 `make ci`, portable Python 174/16 expected skips, focused
  project-check, 실제 Linux/386 loader, 11 checksum과 no-rewrite를, macOS exact는 focused Go/
  project-check, exact Python 174/174, 11 oracle와 no-rewrite를 통과했습니다. 네 project-check와
  네 SQLite matrix leg도 normal/race/CGO-disabled/vet/clean을 모두 통과했습니다. 상세 결과는
  [EVID-20260810-026](TEST_EVIDENCE.md#evid-20260810-026--gdj-0021-github-hosted-completion-documentation-head-10-job-ci)에
  기록했습니다. 현재 exact 8-file evidence/status patch 자체의 hosted CI는 `not run/pending`입니다.

## 확정된 결정

- [ADR-0017](../adr/0017-revision-fenced-migration-lifecycle.md)의 per-step first-write fence와
  atomic successor 안전성 방향을 [ADR-0018](../adr/0018-revision-fenced-migration-lifecycle-product-shape.md)의
  Executor-owned public product shape로 구현합니다.
- Lifecycle은 already-loaded, version-compatible `[]Migration`을 입력으로 받습니다. Source
  document/version handshake와 operation codec는 Accepted
  [ADR-0019](../adr/0019-versioned-migration-definition-source.md)이 contract-only로 정의하며,
  제품 loader는 completed GDJ-0020/[Accepted ADR-0020](../adr/0020-migration-definition-loader-product-shape.md),
  CLI는 그 이후 별도 범위입니다.
- Existing backend port를 widen하지 않고 optional fenced port를 추가합니다. Unsupported backend는
  fail-closed하고 legacy fallback이나 conflict 자동 retry를 제공하지 않습니다.
- Migration별 commit과 last durable state는 ADR-0014를 유지합니다. Lifecycle 전체 outer
  transaction, distributed lock, lease와 crash reconciliation은 제공하지 않습니다.
- Fresh-only bootstrap과 no-op 무생성 경계를 유지합니다. Existing database adoption/repair,
  database copy/restore epoch 정책과 overflow recovery는 후속 범위입니다.
- ADR-0013 canonical ascending planner order를 유지합니다. MIG-052의 six-path DEV-0002 외 final
  state/DB/history/phase 차이는 허용하지 않습니다.
- [ADR-0021](../adr/0021-project-linked-migration-check.md)은 exact `godj.toml`, closed descriptor/
  runner protocol, project-relative flat no-follow discovery, exit/cancel/cleanup과 11 cap을
  contract/test-only 경계로 Accepted합니다. Production CLI/package/runner 구현은 별도 product work가
  소유합니다.

## Accepted source contract 경계

- [ADR-0019](../adr/0019-versioned-migration-definition-source.md)은 caller-provided strict data-only
  JSON v1과 tuple `(definition format 1, loader ABI 1, operation codec 1, Schema IR 2)`을 채택합니다.
- Codec v1은 fully normalized IR v2 `CreateModel`과 non-PK `char`/`boolean` `AddField`만 허용합니다.
  Python/Go executable source, custom operation/callback/raw SQL와 file/module discovery는 비목표입니다.
- SourceID는 non-empty/unique diagnostic precedence handle이며 migration identity나 digest input이
  아닙니다. Semantic definition-set digest는 trust/revision/history fence가 아닙니다.
- MIG-057..064는 atomic batch, canonical digest/error와 existing `Executor.Migrate` reference
  handoff를 synthetic decision oracle로 잠급니다. `conformance/definitionload/**`의 independent
  proof는 계속 test-only이며 product package가 import하지 않습니다. 실제 public API는 별도 leaf
  package `migrations/definition`에 구현됐습니다.
- 이 source tuple은 Q-010의 global CLI/library/generator semver handshake 전체를 해결하지
  않습니다. CLI는 product loader 뒤 별도 orchestration으로 다룹니다.

## Completed GDJ-0020 제품 경계

- Contract baseline은
  `codex/revision-fenced-migration-lifecycle@eecc75f7507414ad6043a090c97b84080ab0fb8b`, activation
  commit은 `5942a0bedd6cca7fe93e52d90219a01193c6f534`, product commit은
  `6172d843a4bb234592cafc176a8d1191933b141c`이고
  [completed work](../../work/0020-migration-definition-loader-product-slice.md)의 exact allowed paths만
  수정했습니다.
- [Accepted ADR-0020](../adr/0020-migration-definition-loader-product-shape.md)의 새 leaf package
  `migrations/definition`, `Source{SourceID,Document}`와
  `Load(...Source) (Set, LoadReport, error)`를 구현했습니다. Zero `Set`은
  canonical empty set입니다.
  Set accessor와 success/error report/failure context는 caller와 alias하지 않는 value/deep-copy
  snapshot이며 raw source document를 보존하지 않습니다.
- Source-owned error는 9개 code만 사용합니다. Resource breach는 새 code 대신
  `reason=resource_limit_exceeded`, stable `Limit`/`Maximum`/`Actual`을 기록하고 semantic reason
  rank에 넣지 않는 stage preflight guard입니다. Existing
  `*migrations.PlanningError`와 `Executor.Migrate` lifecycle error는 wrap/reclassify/retry하지 않는
  raw ownership을 유지합니다.
- Accepted numeric limits는 source 2,048, source ID 1,024 bytes, document 1 MiB, batch 16 MiB,
  JSON depth 64, document JSON values 65,536, batch JSON values 262,144, migration별 dependencies
  2,047, operations 2,048, CreateModel fields 2,048입니다. 합산은 overflow-safe이고 각
  maximum-1/equal/+1, combined-fault와 undecidable-type precedence를 검증했습니다.
- Compatibility mismatch는 semantic container cap보다 먼저 실패하고, tuple이 맞은 valid array의
  cap은 child semantic traversal보다 먼저 실패합니다. Type/discriminator가 잘못돼 cap을 판정할 수
  없으면 resource error를 추측하지 않습니다. Preflight order는 source count → ID bytes → bounded
  ID validation → document/batch bytes before copy → snapshot → depth/value → regular document → tuple
  → dependencies/operations/fields caps → regular semantic → Planner → digest입니다.
- `AddField.model_name`은 local regex를 복사하지 않고 fixed-valid synthetic schema의 `Model.Name`에
  넣어 `ir.Normalize`가 검증합니다.
- Wire Schema IR은 current constant alias가 아닌 literal `2`와 two-way compile drift assertion으로
  잠급니다. `Set.Migrate`는 fresh deep-copied definitions와 caller-provided immutable request
  value만 existing `Executor.Migrate`에 정확히 한 번 전달합니다. Private request snapshot/validation은
  Executor 내부 소유이며 reflection/core API widening은 금지합니다.
- 열 번째 product adapter와 exact 105 contract의 `100 passing + 5 deviation`은 local gate와
  exact product-head hosted gate에서 충족했습니다. Work/ADR 상태는 completed/Accepted입니다.
- Oracle/static/SHA, Python reference scenario와 test-only candidate는 immutable입니다. Python의
  유일한 예외는 manifest status assertion을 `oracle_locked`에서 `passing`으로 바꾸는 것입니다.
  GDJ-0020 product slice 자체에는 existing Ubuntu job의
  `CGO_ENABLED=0 GOARCH=386 go test -count=1 ./migrations/definition` focused step만 추가했습니다.
  아래 GDJ-0021의 별도 workflow expansion은 그 completed product artifact/support claim을 바꾸지
  않습니다.

## Completed GDJ-0021 contract 경계

- Baseline은
  `codex/revision-fenced-migration-lifecycle@53729103651bfc34acc5fe07fb4376d5dd78c204`, activation은
  `fbc3c7cfc2fd779117944b8e2479a6a2bf17fdb5`, implementation은
  `84ddf109c04acd72992b816aa72140c6e748e5f0`입니다. Completed work는
  [GDJ-0021](../../work/0021-migration-project-check-compatibility-contracts.md), decision은
  [ADR-0021](../adr/0021-project-linked-migration-check.md) Accepted입니다.
- 사용자 목표 후보는 DB-free `godj migrations check`입니다. Implicit nearest case-sensitive
  `godj.toml` 또는 `--project <descriptor-file>`을 선택하고, closed descriptor v1의
  `project.package`를 private project runner로 build/run해 linked code가 flat source catalog를 actual
  `definition.Load`에 exactly once 넘기는 외부 의미를 MIG-065..074로 먼저 고정합니다.
- Global build contract는 no-shell/private temp, `-mod=readonly`, `GOWORK=off`, `GOTOOLCHAIN=local`,
  `GOENV=off`, disabled `GOCACHEPROG`와 private TMP/cache/HOME/XDG/telemetry directory입니다. Default
  HOME/config/netrc lookup만 redirect하며 explicit auth/VCS/compiler helper env는 격리하지 않습니다.
  Static temp-base containment만 검사하며 same-user concurrent base rebind/rename fencing은 후속입니다.
  Linked protocol v1은 exact `migrations.check` request와 closed success/failure response를 사용하고
  valid logical response의 transport exit는 0입니다. Public exit 후보는 valid 0, invalid catalog 1,
  arguments/project/config 2, build/filesystem/runner/protocol/internal 3, handled Unix SIGINT cleanup
  130입니다. Loader detail/counter와 raw build/runner diagnostic은 test-only이고 wire/public stderr는
  category/code pair만 전달합니다. Terminal primary는 single coordinator stage barrier에서 SIGINT >
  caller cancel 우선순위로 한 번 commit합니다.
- Source candidate는 project-relative clean root의 immediate case-sensitive `*.godj.json` regular file만
  no-follow로 읽습니다. Root/enumeration order와 무관한 SourceID raw UTF-8 byte order, unsafe matching
  symlink/non-regular fail-closed, hardlink-as-regular와 no recursion으로 잠갔습니다. Bounded read 뒤 mandatory
  post-read identity/I/O check가 simultaneous read/cap outcome보다 우선합니다. Machine oracle metrics는
  exact 24-field closed shape이고 temp/diagnostic/process scalar는 feasibility-only입니다.
- Parsed/accepted project/runner/discovery의 inclusive cap은 descriptor 64 KiB, ancestors 128, roots 256,
  aggregate entries 65,536, sources 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, request/
  response 각 64 KiB, diagnostic stream별 retained prefix 1 MiB의 11개입니다. CPU/RSS/process/time,
  private disk/inode, module/network와 post-cap drain은 bounded claim이 아니며 sandbox/hard timeout도
  제안하지 않습니다. Entry cap은 final source-root entries에만 적용하고 marker/root-component raw-name
  pre-scan의 cumulative entry/name bytes/time은 아직 hard bound가 없습니다.
- MIG-065..074는 Django parity가 아닌 `decision/ADR-0021/derived=false` independent reference입니다.
  완료 결과는 11 set/115 contract/110 ordered cross-binding과 새 10 `oracle_locked`입니다. Product는
  10 adapter/105 contract의 `100 passing + 5 deviation`을 유지하고 새 actual adapter를 만들지
  않습니다.
- DB-free는 orchestration-direct NewPlanner와 GoDj-owned DB/recorder/Executor.Migrate/revision lifecycle
  call 0을 뜻합니다. Actual Load-owned graph validation NewPlanner는 success에서 1회이며 linked user
  package `init()` side effect가 없다는 보장이 아닙니다. Public CLI/project API와 product path는 이번
  work의 금지 경계입니다.
- Cleanup 보장은 normal return/caller context cancel/handled SIGINT에만 적용하며 other fatal signal,
  host crash와 broken user output sink의 structured delivery는 비목표입니다.
- Hosted target은 existing `ubuntu-24.04` x64 full + `macos-15` arm64 exact 두 job을 유지·보강하고,
  `ubuntu-22.04`, `ubuntu-24.04-arm`, `macos-15-intel`, `macos-26`의 required 4-leg project-check와
  동일 4-leg SQLite matrix를 더한 exact 10 job executions입니다. 두 matrix는 fail-fast false/leg 20분,
  Go 1.26.5, label별 expected GOOS/GOARCH exact assertion과 normal/race/CGO-disabled/vet를 실행합니다.
  각 leg는 tracked diff와 porcelain-empty clean worktree로 끝납니다. Static gate는 top-level definitions가
  아니라 2+4+4 expanded execution count를 검증합니다. Windows native contract와
  PostgreSQL/MySQL actual adapter가 없어 해당 green-skip/service-only job은 만들지 않습니다. Future
  backend의 첫 required job은 digest-pinned service image, health check, UTC timezone과 C locale 또는
  명시적으로 승인된 collation, actual query/write/transaction/schema/migration/recorder/
  revision-lifecycle 및 durable restart/persistence contract를 모두 실행해야 합니다. Expected
  contract 수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도
  필수입니다. Adjacent versions는 이후 non-required scheduled matrix로만 분리합니다. Exact
  implementation head의 expanded CI는 run 31320798963에서 10/10 PASS했고, exact 16-file
  completion-documentation head `34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도 run
  31322122760에서 10/10 PASS했습니다. 현재 exact 8-file evidence/status patch 자체의 hosted CI는
  `not run/pending`입니다.

## 현재 차단 요인과 알려진 제한

외부 blocker는 없습니다. GDJ-0021 contract/reference/test-only proof와 independent review,
implementation-head 및 completion-documentation-head exact 10-job hosted CI까지 완료했습니다.
Q-010/Q-012는 public CLI/library/generator semver handshake, production project runner와 DB-aware
migration lifecycle 전체가 아니므로 `Partial`입니다. 현재 EVID-026 append/status 교정의 exact
8-file patch 자체의 hosted CI만 commit/push 전 `not run/pending`입니다. 다음은 의도적으로 아직
구현하지 않은 제품 범위입니다.

- Source discovery/public CLI, writer/upgrade/cache는 GDJ-0020 비목표로 계속 미구현
- Codec v2+, writer/upgrade/cache, executable/custom/data operation과 filesystem/module/remote adapter
- Existing database adoption/repair와 unknown commit reconciliation
- PostgreSQL/MySQL 등 non-SQLite fenced backend, multi-DB router와 distributed coordination
- Live schema drift, non-cooperating direct SQL writer, pre-cutover completed ABA와 crash repair

## 다음 정확한 작업

통합 담당자는 EVID-026 append/status 교정의 exact 8-file patch를 Markdown/frontmatter/link/
exact-scope 검사 뒤 같은 Draft PR #1에 commit/push하고, 그 **evidence-patch exact head**의 10-job
CI를 별도 evidence로 확인합니다. 현재 active/ready work는 없습니다. 다음 product slice를 시작하려면
public CLI/project API/production runner 경계를 별도 work/ADR로 activation하며 GDJ-0020의 pure
`Source` loader에 path/I/O를 소급해 넣거나 test-only `conformance/projectcheck` shape를 제품 API로
조용히 승격하지 않습니다.

## 작업 재개 체크포인트

- 현재 completion-documentation/hosted-tested base: branch
  `codex/revision-fenced-migration-lifecycle@34ae58fc2490deb8f884a0b5591520b11bae8669`
- 현재 working tree: GDJ-0021 EVID-026 append/status 교정의 exact 8-file diff; 아직
  commit/push/hosted CI 전
- 최근 완료 work:
  [GDJ-0021](../../work/0021-migration-project-check-compatibility-contracts.md)
- active work: 없음
- ready work: 없음
- current decision: [ADR-0021](../adr/0021-project-linked-migration-check.md) Accepted
- 현재 reference 분류: 11 set/115 contract/110 ordered cross-binding; MIG-065..074 exact 10
  `oracle_locked`
- 현재 제품 분류: 10 product adapter/105 product contract, `100 passing + 5 deviation`
- Q-010/Q-012: `Partial`; product CLI/project runner/DB-aware check는 미구현
- 전체 local gate: `make ci`; focused project-check normal/race/CGO-disabled/vet/count-20와 exact
  Python 174/174 PASS
- Portable CI equivalent: `make ci`
- Hosted CI: GDJ-0021 implementation head run 31320798963과 exact completion-documentation head
  run 31322122760의 exact 10 jobs PASS; 현재 evidence/status patch head는 `not run/pending`
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 가장 위험한 과장: completion-documentation-head PASS를 아직 push하지 않은 EVID-026
  evidence/status patch head PASS로 재귀 사용하거나, Accepted GDJ-0021 contract/test-only candidate를
  implemented public CLI/project API로 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
