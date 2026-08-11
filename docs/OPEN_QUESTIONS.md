# 핵심 미결정 사항

- 상태: Active register
- 마지막 검토: 2026-08-11

이 문서의 항목은 초안 예시를 확정 API로 오해하지 않도록 관리합니다. 결정이 나면 개별 ADR로 옮기고 여기에는 결과 링크만 남깁니다.

| ID | 우선순위 | 결정 시점 | 질문 |
|---|---|---|---|
| Q-006 | Resolved | GDJ-0006 | ADR-0011의 Manager Save, concrete typed option/field mask와 generated explicit-key helper로 MOD-008..019 Verified |
| Q-007 | Resolved | GDJ-0008 | ADR-0012 ownership/API와 QRY-011..021 제품 adapter가 Verified; 총 45개 contract passing |
| Q-010 | Partial | GDJ-0022 completed / full handshake 후속 | Exact public project entrypoint와 전역 check CLI는 implemented and exact 18 hosted accepted; generator/library semver·repair는 open |
| Q-011 | Partial | GDJ-0008/M5+ | QuerySet evaluation subset은 ADR-0012와 race/cancellation test로 해결; request/transaction/hook 범위는 후속 단계에서 결정 |
| Q-012 | Partial | GDJ-0022 completed | MIG-047..074 product subset은 implemented/passing and exact 18 hosted accepted; writer/upgrade/custom operation/DB-aware execution/non-SQLite/crash recovery는 open |
| Q-013 | Partial | GDJ-0029 active / relation facade 후속 | Accepted relation semantics와 bounded primitives; Proposed one-hop eager 및 canonical project facade·relation-aware chaining·FK mutation policy는 open |
| Q-014 | P2 | M5 전 | DTL parser/runtime 호환 수준과 method exposure 정책은 무엇인가 |
| Q-015 | P2 | M6 전 | Admin에서 보존할 흐름과 새로 설계할 UI/DOM/CSS 경계는 무엇인가 |
| Q-016 | P2 | M7/M8 전 | DRF와 Channels의 정확한 reference version과 호환 범위는 무엇인가 |
| Q-017 | P1 | GDJ-0029 public surface acceptance/API freeze 전 | relation-aware project facade, pre-1.0 공개 API와 generated code upgrade 정책은 무엇인가 |
| Q-018 | P2 | 공개 배포 전 | Django trademark와 비공식 프로젝트임을 어떤 이름·고지 정책으로 다루는가 |

## M0에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-001 | 선언 package와 generated target을 import graph에서 분리 — [ADR-0006](adr/0006-codegen-input-package-boundary.md) |
| Q-002 | exact runtime fingerprint와 `uv.lock` hash를 profile에서 검증 — [Compatibility Lab](../conformance/README.md) |
| Q-003 | M0에서 strict JSON protocol v1과 explicit adapter를 채택한 뒤, GDJ-0003에서 contract phase를 결속하는 v2로 명시 승격 — [protocol](../conformance/internal/protocol) |
| Q-004 | 독립 시나리오와 upstream 파생물을 구분하고 Django BSD 고지를 보수적으로 포함 — [Licensing](LICENSING.md) |

## M1에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-005 | `orm` 소유 generic interface + generated zero-state concrete descriptor, generation/compile 시점 freeze — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |
| Q-008 | ordered input을 `ParseDynamic`에서 즉시 typed predicate 또는 error로 변환 — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |
| Q-009 | consumer-owned interface와 external module compile + `go list` dependency gate — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |

## M2에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-006 | generated create/patch와 nullable change state는 ADR-0009, mutable instance Save/typed mask/explicit key orchestration은 [ADR-0011](adr/0011-m2-save-lifecycle-orchestration.md); MOD-001..019 verified |
| Q-012 core | preflighted ProjectState/Operation/Executor와 한 transaction의 SQLite editor/recorder — [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md) |

## Q-001 — Codegen bootstrap — Resolved

초안의 임시 runner import 방식은 schema package가 오래된 generated type 때문에
compile되지 않으면 동작하지 않습니다. 실행 spike에서 rename/delete, stale 사용자
메서드, last-good output 보존을 검증하고 선언/generated package 분리를
채택했습니다. Project runner 형태는 Q-010에서 계속 다룹니다.

## Q-006 — Nullable와 변경 추적

M1 read model은 nullable CharField에 `*string`을 사용해 `nil`, `ptr("")`, 일반 값을
구분합니다. MOD-002~004 oracle과 GDJ-0004 구현을 거쳐 generated immutable
create/patch builder, `Change[T]`/`NullableChange[T]`와 Manager write API를
[ADR-0009](adr/0009-m2-explicit-write-change-state.md)에서 채택·검증했습니다. Django가
기본 `save()`에서 실제 dirty tracking을 하는지 추측하지 않고, instance save/new/loaded,
`update_fields`, force flag, explicit PK와 rollback 의미를
[GDJ-0005](../work/0005-save-lifecycle-compatibility-contracts.md)의 MOD-008..019로
고정했습니다. Fully loaded default save는 field 전체를 쓰며 dirty-only가 아닙니다.
Go에서는 [ADR-0011](adr/0011-m2-save-lifecycle-orchestration.md)에 따라 concrete
`SaveOption[M]`, sealed `WritableField[M]`, generated explicit-key constructor와
`Manager[M].Save`를 사용합니다. Generated instance method는 model field `Save`와의
충돌 때문에 만들지 않으며, MOD-008..019가 이 경계로 모두 통과했습니다.

## Q-007 — QuerySet cache

불변 plan과 instance result cache를 분리해야 합니다.
[GDJ-0007](../work/0007-queryset-evaluation-cache-compatibility-contracts.md)에서 chain,
반복/빈/실패 평가, iterator/Count/Exists/fresh clone의 Django 외부 동작을 먼저
QRY-011..021의 exact 계약으로 고정했습니다. 당시 oracle은 `oracle_locked`이고 제품은
intentionally uncached였습니다. Value-copy QuerySet의 cache ownership, cached result alias,
동시 `All`, waiter cancellation과 Go terminal 표면은 그 결과를 입력으로
[ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에서 Accepted했습니다.
[GDJ-0008](../work/0008-queryset-evaluation-cache-product-slice.md)은 이를 제품 unit/compile/
race/differential test로 구현해 QRY-011..021 모두를 `passing`으로 검증했습니다. 따라서
Q-007은 해결됐습니다. Q-011은 QuerySet evaluation subset만 해결됐고 request,
transaction-bound session과 hook의 goroutine ownership은 후속 단계에 남습니다.

## Q-010 — CLI와 project/library version handshake

[완료된 GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)와
[Accepted ADR-0019](adr/0019-versioned-migration-definition-source.md)은 caller-provided migration
definition document와 consumer 사이의 exact tuple
`(definition format 1, loader ABI 1, operation codec 1, Schema IR 2)`를 MIG-060 `environment`
contract로 검증합니다. 이 handshake는 operation decode/construction 전에 fail-closed하는 source
ABI이며 global `godj` CLI, project library와 generator의 semver resolution이 아닙니다.

GDJ-0019는 file/module discovery, project build, generated Go runner, CLI exit code나 upgrade
command를 결정하지 않았습니다. 완료된
[GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)과 Accepted
[ADR-0020](adr/0020-migration-definition-loader-product-shape.md)은 explicit caller bytes의
bounded product loader만 구현·검증했습니다. File/module/FS discovery나 CLI를 붙이지 않으며,
CLI/project binary가 어떤 version 정보를 교환하고 mismatch/old generator/stale output을 어떻게
복구할지는 별도 work/ADR로 결정해야 합니다. 따라서 Q-010은 계속 Partial입니다.

완료된 [GDJ-0021](../work/0021-migration-project-check-compatibility-contracts.md)과 Accepted
[ADR-0021](adr/0021-project-linked-migration-check.md)은 full handshake보다 작은 DB-free
`godj migrations check` contract를 test-only로 검증했습니다. Proof는 exact `godj.toml` descriptor v1의
project package를 private binary로 build하고 strict runner protocol v1에서
`migrations.check`를 요청합니다. Descriptor/runner wire version mismatch와 public exit,
`-mod=readonly`/`GOWORK=off`/`GOTOOLCHAIN=local`/`GOENV=off`, private TMP/cache/HOME/XDG/telemetry와
handled SIGINT cancel/reap는 MIG-065..074의 decision reference 범위입니다. Runner wire는
category/code pair만 전달하고 loader file/pointer detail은 test-only이며 user diagnostic protocol
확장은 후속입니다. 11 cap은 parsed/retained 경계일 뿐 build/runner CPU·시간·disk/network sandbox가
아닙니다.

Implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`의 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)은 existing
`ubuntu-24.04` x64 full/`macos-15` arm64 exact 2개, `ubuntu-22.04` x64/
`ubuntu-24.04-arm` arm64/`macos-15-intel` x64/`macos-26` arm64 project-check 4개와 동일 좌표의
SQLite normal/race/CGO-disabled/vet 4개, exact `2 + 4 + 4 = 10` job을 모두 통과했습니다. 각 matrix
leg는 expected GOOS/GOARCH, `fail-fast: false`, no `continue-on-error`와 final tracked-diff/
porcelain-empty clean worktree gate를 만족했습니다. Exact 16-file completion-documentation commit
`34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)의 같은 exact 10 job을
모두 통과했습니다. EVID-026 append/status 교정 commit
`f7fbbd50465a610ed9492227909eece524455f15`도 별도 run `31322959993`의 같은 exact 10 job을
통과했습니다. GDJ-0022 workflow는 product 4와 exact Python
3.12.13/3.13.15/3.14.3/3.14.7 compatibility 4를 더한 exact 18 required execution으로 확장됐고 fix head
run `31329294154`에서 18/18 성공했습니다. Initial run의 네 Python pre-test uv assertion failure/cancel과
fix는 EVID-028에 기록했습니다. Portable/compatibility는 uv
0.12.3, historical exact darwin oracle만 embedded profile의 uv 0.10.12를 사용합니다. Windows
native contract가 없고 actual backend는 SQLite뿐이므로 Windows green skip과 PostgreSQL/MySQL
service-only CI는 support evidence로 만들지
않습니다. Future backend는 digest-pinned service image, health check, UTC timezone과 C locale 또는
명시적으로 승인된 collation, actual query/write/transaction/schema/migration/recorder/
revision-lifecycle 및 durable restart/persistence contract를 먼저 required job으로 검증합니다.
Expected contract 수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도
필수이며 adjacent versions는 scheduled/non-required로 분리합니다.

이 GDJ-0021 증거만으로는 public global CLI/project package API, production project binary entrypoint,
persistent runner cache, generator/library semver resolution이나 stale output repair를 확정하지 않았습니다.
SIGTERM/other fatal signal, crash stale-temp scavenging과 broken stdout/stderr sink delivery도 아직
결정하지 않습니다.

Completed [GDJ-0022](../work/0022-migration-project-check-product-slice.md)와 Accepted
[ADR-0022](adr/0022-project-runtime-and-global-migration-check.md)는 Q-010 중 exact 두 global argv와
public project-linked entrypoint를 제품화했습니다. Exact API는 explicit
`project.Config{MigrationDefinitionRoots: ...}`와
`project.Run(ctx, config, argv, stdin, stdout) error` 두 export이며 global mutable registration과 public
protocol/report는 만들지 않았습니다. Exact 18 hosted acceptance는 완료됐지만 full library/generator
semver, stale repair와 installed runner lifecycle은 계속 open이므로 Q-010은 이 work 뒤에도
Partial입니다.

## Q-012 — Migration format과 실행 수명주기

MIG-001~004와 GDJ-0004 제품 구현을 거쳐 state/operation/executor/schema editor/
recorder core를 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md)에서
채택·검증했습니다.
[GDJ-0009](../work/0009-migration-planning-compatibility-contracts.md)는 제품 graph를 먼저
만들지 않고 MIG-005..016으로 dependency/applied-state 기반 forward/backward plan,
multi-target 중복 제거와 잘못된 graph/history 오류를 contract-only로 고정했습니다.

[ADR-0013](adr/0013-immutable-migration-planner.md)은 `ProjectState`와 applied history를
분리하고 immutable identity graph의 zero-I/O Planner를 사용하기로 결정했습니다.
[GDJ-0010](../work/0010-immutable-migration-planner-product-slice.md)은 이 경계와 actual
adapter를 구현해 MIG-005..016을 모두 `passing`으로 검증했습니다. MIG-012는 caller target
order와 dependency precedence만 잠그며, incomparable sibling의 Django private DFS
tie-break는 호환 계약이 아닌 Go deterministic 정책입니다.

[GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md)은
multi-migration execution과 partial commit/failure stop 의미를 MIG-017..026의 여섯 번째
exact set으로 잠갔습니다. Django backward의 `schema_then_record` 때문에 정상 backward
세 계약의 transaction model과 recorder failure 한 계약의 DB state/phase가 GoDj 기존
same-transaction reverse와 다릅니다.

완료된 [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md)는 full zero-I/O
preflight, migration별 existing Apply/Unapply commit과 last durable state를 가진 최소
`ExecutePlan`을 구현했습니다. Same-transaction reverse는
[ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)와
[DEV-0001](DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
Accepted/Verified 결정이며 GDJ-0012 완료 당시 제품 상태는
`63 passing + 4 deviation`이었습니다.

완료된 [GDJ-0013](../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)은
새 process/executor가 durable recorder에서 applied identity를 읽고 남은 plan을 계산하는
의미를 MIG-027..036의 10 `oracle_locked` 계약으로 고정했습니다. Absent read는 table을
만들지 않고, unknown legacy row는 보존하며, known inconsistent history는 explicit
migrate-style preflight에서 plan 전에 거부합니다.

완료된 [GDJ-0014](../work/0014-recorder-backed-restart-planning-product-slice.md)와 Accepted
[ADR-0015](adr/0015-recorder-backed-applied-state.md)는 transaction write interface와 분리된
raw recorder read port, core `LoadAppliedState`와 `Planner.CheckHistory`, SQLite read-only
reader를 제품화했습니다. Fresh file-backed restart를 포함한 MIG-027..036은 10
`passing`이며 GDJ-0014 완료 당시 제품 분류는 `73 passing + 4 deviation`이었습니다.
Recorder key만으로
`ProjectState`를 재구성할 수 없고 read/execution 사이 lock도 없으므로 public
restart/migrate convenience API는 아직 만들지 않습니다.

완료된
[GDJ-0015](../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)는
MIG-037..046으로 historical `ProjectState` reconstruction의 외부 의미를 여덟 번째 exact
set에 고정했습니다. Explicit empty와 omitted latest는 다른 request mode이고, target
before/after는 dependency closure의 포함 위치를 구분합니다. Applied projection은 unrelated
known branch를 포함하되 unknown legacy identity를 schema로 만들지 않습니다. 새 10개는
`oracle_locked`이고 GDJ-0015 완료 당시 제품 adapter는 fail-closed했습니다.

[ADR-0016](adr/0016-historical-project-state-reconstruction.md)은 Accepted이며, 완료된
[GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)은 explicit
request API, immutable definition ownership/clone, Planner graph kernel 공유와 structured error를
구현했습니다. MIG-037..046은 10 `passing`, GDJ-0016 완료 당시 전체 제품 분류는
`83 passing + 4 deviation`입니다.
Applied adapter는 real SQLite recorder snapshot을 쓰지만 reconstructor core는 backend I/O가
없는 pure replay 경계입니다.

Explicit data-only source encoding과 bounded loader는 GDJ-0019/0020에서 결정·구현했습니다.
Source discovery/listing, writer/upgrade, data/custom/raw-SQL operation ABI, graph merge/squash/
optimizer, multi-process lock와 crash recovery는 여전히 결정하지 않았으며 public CLI 전에 별도
ADR과 contract가 필요합니다. Recorder read/planning, historical-state와 explicit-source loader
제품 subset을 완료해도 Q-012 전체 해결을 뜻하지 않습니다.

완료된
[GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
loader/CLI보다 lifecycle의 observable meaning과 stale-snapshot safety feasibility를 먼저
분리합니다. MIG-047..056은 fresh/prefix/no-op latest, named forward/reverse, app zero target,
unknown legacy, explicit inconsistent-history preflight, middle failure와 fresh durable restart의
아홉 번째 exact set으로 고정했습니다. MIG-054의 preflight 소유자는
`MigrationExecutor.migrate()` 자체가 아니라 public command orchestration의
`loader.check_consistent_history(connection) → target/plan → migrate` 순서이며
`plan_invoked=false`와 transaction/DDL/write 0을 관찰합니다. MIG-056은 `:memory:` 재사용이
아니라 temporary file database close/reopen과 fresh connection/loader/executor를 요구합니다.
Backward MIG-051/052는 abstract step outcome과 final schema/recorder만 비교하고 physical
transaction topology는 기존 DEV-0001/ADR-0014에 남깁니다.

[Accepted ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)은 recorder identities와
opaque freshness revision을 한 snapshot으로 읽고 각 migration transaction 안에서 첫 DDL/write
전에 expected token을 검증합니다. SQLite spike는 persistent epoch와 monotonic revision을
후보로 검증했고 fingerprint는 direct non-ABA recorder drift를 잡는 보조 gate로만 사용했지만,
GDJ-0017 완료 당시 제품 storage와 token encoding은 open이었습니다. Outer transaction 없이
migration별 durable commit을 유지하고 stale conflict는 current step mutation 0/last-durable
state로 fail-closed하며 semantic auto retry를 하지 않습니다. Spike가 optional capability와
unsupported fallback 금지를 검증했지만 당시 public coordinator/backend API와 final error
taxonomy는 후속 제품 work로 남겼습니다.

GDJ-0017이 끝났어도 file/source encoding, operation codec/version, data callback, public
coordinator/CLI, lease/fairness, process-kill crash reconciliation과 non-SQLite backend는 여전히
Q-012 후속입니다.

완료된
[GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 Q-012 중 already-loaded
definition lifecycle을 닫았습니다. Public `Executor.Migrate(ctx, definitions, request)`는 explicit
latest/targeted tagged request와 Executor-owned optional backend session을 사용합니다. Session은
identities와 private epoch/revision/fingerprint를 exact-one atomic snapshot에 결속하고 call 사이
connection을 pin하지 않으며 mandatory Close/state machine을 가집니다. 각 SQLite step은 pinned
connection의 `BEGIN IMMEDIATE` 안에서 first-write fence, schema, recorder와 successor token을
commit합니다. Unsupported backend fallback과 existing recorder 자동 adoption은 없습니다.

Dedicated fenced transaction은 rolled-back/committed/unknown durability를 구분합니다.
`CommitRolledBack`은 confirmed state/token을 advance하지 않고 SQLite session을 poison하며,
unknown과 함께 semantic retry를 금지합니다. Accepted ADR-0013의 canonical ascending plan도
그대로여서 MIG-052의 final state/schema/history는 exact match이고 ordered plan/steps 여섯 path만
DEV-0002입니다. Lifecycle 9 `passing` + 1 `deviation`을 더한 GDJ-0018 완료 당시 제품 분류는
`92 passing + 5 deviation`이었습니다.

Exact A2의 empty-table `BooleanField(default=false)`는 logical state에 default를 보존하면서
physical persistent default 없이 추가합니다. Nonempty table backfill/rebuild는 계속
unsupported입니다.

[완료된 GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)은 Q-012 중
explicit caller-provided source/versioned-loader 의미만 MIG-057..064의 contract로 분리했습니다.
Strict data-only JSON v1, tuple `(1,1,1,2)`, fully normalized IR v2,
`CreateModel`/non-PK `char`·`boolean` `AddField` closed codec, atomic load, canonical digest/error와 existing
`Executor.Migrate` reference handoff를 Accepted ADR-0019 decision oracle로 고정했습니다. 이는
Django Python file ABI exact compatibility가 아니라 Go redesign이며, 새 8개는
GDJ-0019 완료 당시 `oracle_locked`였습니다. MIG-064도 당시에는 Go handoff 구현이나 제품 loader
지원을 뜻하지 않았습니다.

완료된 [GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)은 Accepted ADR-0020의
`migrations/definition` explicit `Source`/`Load`/zero `Set`/immutable report를 구현했습니다.
Exact cap은 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, JSON depth 64,
document values 65,536, batch values 262,144, dependencies 2,047, operations 2,048,
`CreateModel` fields 2,048입니다. Strict scanner의 closed JSON/RFC 6901 ordering, loader-owned
snapshot/deep copy와 source-owned 9-code/resource context를 검증하고, raw Planner/lifecycle error를
wrap/reclassify/retry하지 않습니다. Literal Schema IR 2는 two-way compile drift gate로 잠급니다.

열 번째 actual adapter가 MIG-057..064 decision-reference 8개를 `passing`으로 전환해 GDJ-0020 당시 제품
분류는 10 adapter/105 contract의 `100 passing + 5 deviation`이었습니다. Product commit
`6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1 run 31309152526의 Ubuntu/macOS
두 job과 실제 Linux/386 focused runtime을 통과했습니다. Completion-documentation commit
`a5422f2c1ba5db34986564fc065e4b8e28ef0115`도 별도 run 31310002784의 Ubuntu/macOS 두 job에서
통과했고, EVID-023 append/status 교정 baseline
`53729103651bfc34acc5fe07fb4376d5dd78c204`도 별도 run 31310606332의 Ubuntu/macOS 두 job에서
통과했습니다. GDJ-0021 implementation head
`84ddf109c04acd72992b816aa72140c6e748e5f0`도 별도
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)의 exact 10 job에서
통과했습니다.

File/directory/module/remote discovery, public CLI, writer/upgrade/cache, executable/custom/data/
raw-SQL operation, global CLI/project handshake, adoption/repair command, copy/restore epoch,
crash reconciliation과 PostgreSQL/MySQL 등 non-SQLite backend는 계속 Q-012/Q-010의 open
범위입니다.

GDJ-0021은 위 open 범위 중 project-relative flat file discovery와 check-only process boundary만
MIG-065..074로 분리해 완료했습니다. Accepted contract는 linked project code가 clean root list를 제공하고
case-sensitive `*.godj.json` immediate regular files를 no-follow/byte order로 읽어 actual
`definition.Load`를 정확히 한 번 호출하는 의미입니다. 새 10개는 GDJ-0021 완료 당시
`oracle_locked`였고 제품 10 adapter/105 contract의 `100 passing + 5 deviation`은 바뀌지 않았습니다.

이 check의 DB-free 주장은 GoDj-owned DB/recorder/lifecycle call 0으로 제한됩니다. User `init()` side
effect, recursive/module/embed/remote discovery, writer/upgrade, DB/applied-history check와 actual migrate
execution은 해결하지 않습니다. 따라서 GDJ-0021이 완료됐어도 Q-012/Q-010 전체는 Partial입니다.

Completed GDJ-0022는 이 reference를 independent product global/linked/protocol
kernel과 actual adapter로 구현했습니다. Flat filesystem discovery는 included dependency지만
writer/upgrade와 DB-aware lifecycle은 계속 제외합니다. GDJ-0022 완료 당시 제품 분류는 11 adapter/115
contract의 `110 passing + 5 deviation`이었고 exact 18 hosted acceptance도 완료됐습니다. Completed
GDJ-0025의 REL-001 metadata와 REL-004 required predicate actual까지 포함한 GDJ-0025 완료 시점 aggregate는
12 adapter/127 contract의 `112 passing + 5 deviation + 10 oracle_locked`입니다.
PostgreSQL/MySQL job은 actual backend contract 전까지 만들지 않습니다.

## Q-013 — 관계 API

초안의 `RelationField[Post]`에는 target type이 없지만 `PostFields.Author.Name`을 사용합니다. symbolic relation binding, target descriptor, generated loader, reverse relation과 import cycle을 한 설계로 검증해야 합니다.

Completed [GDJ-0023](../work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md)과
Accepted [ADR-0023](adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)은 이 질문의 architecture
부분을 contract/test-only evidence로 결정했습니다. Go package/type pointer가 아닌 stable symbolic target
`(app, model)` identity와 source model/field-owned relation declaration, atomic all-app project binder의
target/reverse resolution, generated source package 사이 target-package direct import 금지와 project bridge
ownership, typed/dynamic relation path의 같은 immutable AST 수렴과 unresolved/collision의 pre-I/O
fail-closed를 채택합니다.

Completed [GDJ-0024](../work/0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md)와
Accepted [ADR-0024](adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)는 metadata-only
product shape를 더 좁혀 구현·검증했습니다. `ir.FormatVersion=2`/existing bytes를 보존하고 exact
`RelationFormatVersion=3` ForeignKey arm과 DSL, mixed v2 target/v3 source app의 additive
`GoDjRelationSchema` companion/project bridge, one-schema-per-app atomic `orm.BindProject`와 reachable
structured errors를 사용합니다. Existing migration tuple `(1,1,1,2)`는 relation을 계속 거부합니다.
REL-001만 제품 metadata actual로 만들고 REL-002..012는 oracle-locked/not-implemented로 유지합니다.

Completed [GDJ-0025](../work/0025-forward-foreign-key-predicate-product-slice.md)와 Accepted
[ADR-0025](adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)는 Q-013 중 required one-hop exact
predicate, shared immutable path와 SQLite reusable INNER JOIN만 구현·검증했습니다. REL-001/004 actual 2/12와
exact implementation-head 26/26 hosted acceptance를 통과했지만 model object wrapper/loader/cache,
nullable/reverse typed/query surface, `select_related`/`prefetch_related`, write/delete/DDL/migration codec와
broader generator ABI는 아직 결정/구현하지 않았습니다. 이 open breadth 때문에 Q-013은 `Resolved`가 아니라
`Partial`입니다. REL-003 object cache와 REL-006 nullable access/isnull은 의도적으로 별도 decision으로
남깁니다.

Completed [GDJ-0026](../work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md)과 Accepted
[ADR-0026](adr/0026-forward-foreign-key-object-cache-and-nullability.md)은 그 두 bounded boundary를 함께 동결합니다.
Original descriptor interface를 retain하지 않는 sealed immutable snapshot/storage, project-owned opaque pointer
wrapper와 QuerySet-backed target PK limit-2 cache, nullable local NULL fast path, `source_key` relation terminal
scope와 SQLite JOIN-0 trim을 선택했습니다. Existing project-query v1은 바꾸지 않고 새 object aggregate가
typed/dynamic reviewer isnull을 소유합니다. Exact implementation head `5be46141...`의 run `31370313755`은
26/26 jobs·326/326 recorded steps를 통과했습니다. Reverse, eager/prefetch, write/delete/DDL/migration과
broader target/backend가 열려 있어 Q-013은 계속 `Partial`입니다.

Completed [GDJ-0027](../work/0027-reverse-foreign-key-accessor-and-lookup-product-slice.md)과 Accepted
[ADR-0027](adr/0027-reverse-foreign-key-accessor-and-lookup.md)은 Q-013 중 REL-005-only reverse exact lookup와
owner-instance related-set surface를 고정·검증했습니다. Query-only `BindReverse`와 PK-capability
`BindReverseObject`를 분리하고, declaration-centric reverse path 및 project-only query/object aggregates를
사용합니다. Exact implementation head `7db68415...`의 run `31419940399`는 26/26 hosted gate를 통과했고
REL-005를 `passing`으로 전환했습니다.
REL-012 prefetch/IN batching/warm publication, REL-009..011 eager path, write/delete/DDL/migration과 broader
backend가 계속 열려 있으므로 Q-013은 `Partial`입니다.

Completed [GDJ-0028](../work/0028-reverse-foreign-key-prefetch-product-slice.md)과 Accepted
[ADR-0028](adr/0028-reverse-foreign-key-prefetch.md)은 그중 REL-012-only two-stage reverse prefetch를 별도로
동결·검증했습니다. Existing QuerySet이 primary owner query를 소유하고 additive immutable IN condition,
`ReversePrefetch.Load`, sealed source-FK grouping과 private ready `RelatedSet` publication이 batch stage를
소유합니다. Generated project-only companion은 concrete owner/source wrapper를 input order로 반환하되 모든
row 검증 뒤에만 `.Posts().All()` cache를 공개합니다. Exact implementation head `4858ab88...`의 run
`31432551159`는 26/26 hosted gate를 통과해 product를 exact `116 + 5 + 6`, relation 6/12로 전환했습니다.
Custom Prefetch, filter/order 소비, eager REL-009..011, write/delete/DDL/migration과 broader backend가 남아
Q-013은 계속 `Partial`입니다.

Active [GDJ-0029](../work/0029-one-hop-forward-select-related-product-slice.md)과 Proposed
[ADR-0029](adr/0029-one-hop-forward-select-related.md)는 REL-009/010/011을 required INNER, nullable LEFT OUTER,
reverse multi-valued pre-I/O rejection의 indivisible packet으로 좁혔습니다. App-local projection scanner,
singular immutable relation projection, existing object factory에 붙는 All-only eager bridge와 shared
typed/dynamic resolver를 제안하지만 현재는 activation state이며 구현·호환 acceptance가 아닙니다.
Multiple/nested/no-argument/reverse eager, OneToOne/ManyToMany, write/delete/DDL/migration과 broader backend는
결정하지 않았으므로 Q-013은 계속 `Partial`입니다.

Django 6.1의 관계 의미는 기본 reference입니다. Raw FK와 관계 accessor 분리, 미조회/NULL/loaded 구분, 첫 접근
cache, eager/prefetch의 동일 cache warming, reverse manager와 조회 origin DB 유지가 기준입니다. GoDj는 Python
descriptor/runtime registry/예외 구현을 복제하지 않고 explicit `context.Context`/`error`, backend/session binding,
Go 값 복사 규칙과 codegen/project bridge로 번역합니다.

최종 사용자 후보는 `project.Using(backend)`로 한 번 결합한 facade가 relation-aware project model을 직접
반환하고 lazy/eager 모두 `Author(ctx)` 같은 accessor를 공유하는 형태입니다. 현재 `orm.BindProject`, generated
`BindObjects`/`From`과 GDJ-0029 eager bridge는 검증 가능한 low-level building block이며 canonical application
API가 확정됐다는 뜻이 아닙니다. FK mutation/cache invalidation, scalar access, forward/reverse chaining과 exact
facade/manager/selector 이름은 Q-017에서 계속 open입니다.

## Q-017 — 공개 API와 generated upgrade

API freeze 전 external compile-usability spike로 다음을 검증합니다.

- Project facade의 exact name과 one-time backend/session binding
- Raw app model과 relation-aware project model의 역할, scalar field 접근과 명시적 unwrap
- Lazy/eager의 동일 반환 타입·accessor와 forward/reverse chaining
- Filter/OrderBy/Limit 뒤에도 facade type과 relation state가 유지되는지
- `AuthorID` 변경, relation 설정과 cache 무효화 규칙
- Exact transaction session, 값 복사/clone, JSON과 사용자 정의 model method
- Generated identifier 변경의 deprecation/upgrade 정책

저수준 `Bind*`, `From`, `ForwardSelect*` API의 존재만으로 최종 사용자 API가 확정됐다고 보지 않습니다.

새 작업이 이 표의 질문에 의존하면 추측으로 확정하지 말고 작업 문서에 명시하고 필요한 ADR/prototype을 먼저 만듭니다.
