# GoDj 로드맵

- 상태: Accepted direction
- 현재 단계: GDJ-0025 REL-004-only forward predicate/SQLite INNER JOIN completed; implementation-head
  exact-26 EVID-040과 completion-documentation-head exact-26 EVID-041 완료, ADR-0025 Accepted,
  Q-013 Partial. 현재 active/ready work 없음; EVID-041/final-status patch 자체의 exact-head CI pending
- 현재 제품 기준: 12 adapter/127 contract의 `112 passing + 5 deviation + 10 oracle_locked`,
  relation actual REL-001/004 2/12
- 마지막 검토: 2026-08-10

로드맵은 계층별 골격을 오래 만든 뒤 마지막에 연결하는 방식이 아니라, **호환 계약을 통과하는 수직 단면**을 넓혀 갑니다.

## 공통 완료 gate

각 milestone은 다음을 충족해야 완료됩니다.

- M0는 범위 내 reference contract가 모두 `oracle_locked`, M1 이후는 대상 contract가
  모두 `passing` 또는 승인된 `deviation`
- 미지원 기능을 조용히 무시하는 경로가 없음
- external consumer 관점의 compile test가 통과함
- 오류와 실패/rollback 경로가 검증됨
- 실제 명령과 checkout이 test evidence에 기록됨
- CURRENT, work 문서, implementation matrix가 같은 상태를 가리킴
- 새 장기 결정은 Accepted ADR 또는 명시적 Proposed 상태를 가짐

## M0 — Compatibility Lab

목표: 구현 전에 기준 행동과 비교 도구가 거짓 양성 없이 작동함을 증명합니다.

- Django 6.1, Python, SQLite, timezone, locale exact profile lock
- 8~12개 초기 contract와 upstream provenance
- Django scenario runner와 deterministic oracle
- normalizer와 comparator unit/property tests
- GoDj 미구현 상태를 명시적으로 표현하는 runner/protocol
- codegen bootstrap 실패 사례를 재현하는 작은 architecture spike
- CI에서 manifest validation과 Django reference suite 실행

상태: GDJ-0001에서 exact darwin/arm64 oracle과 portable CI validation을 구현하고
로컬 gate를 통과했습니다. 일반 hosted CI는 다른 platform에서 exact oracle을
재생성했다고 주장하지 않습니다.

상세 범위: [GDJ-0001](../work/0001-compatibility-lab.md)

## M1 — Model-to-Query Walking Skeleton

목표: 한 모델의 의미가 선언부터 실제 SQLite 결과까지 흐릅니다.

```text
Schema DSL → normalized IR → deterministic codegen
→ Manager[Article] / QuerySet[Article]
→ typed + dynamic lookup → same AST
→ SQLite compiler/executor → differential result
```

범위는 `AutoField`, `CharField`, `BooleanField`, 필요한 최소 nullable field, exact/ASCII icontains/AND/order/limit/isnull로 제한합니다. migration engine 대신 test-only schema provisioner를 허용할 수 있습니다. public API는 이 단면의 compile usability와 contract 통과 후 확정합니다.

상태: GDJ-0002에서 이 범위의 Schema-to-SQLite 수직 단면과 11개 Django differential
계약을 통과했습니다. 범위 밖 ORM 기능이나 Django 전체 호환을 뜻하지 않습니다.

## M2 — Write Lifecycle + Migration

- create/insert, loaded/new/dirty state, save/update/delete
- transaction과 context cancellation
- project/model/historical state
- CreateModel, Add/Alter/RemoveField
- migration recorder, graph, lock, forward/backward, failure rollback

GDJ-0003은 write/schema/transaction reference 계약을 별도 set으로 잠갔고,
[GDJ-0004](../work/0004-write-migration-walking-skeleton.md)는 generated write API,
SQLite transaction과 최소 ProjectState/Executor/editor/recorder 제품 단면으로
MOD-001..007과 MIG-001..004를 통과했습니다.

상태: M2 전체는 아직 완료되지 않았습니다. Mutable instance `Save()`,
loaded/new/force/explicit PK와 rollback의 외부 의미는
[GDJ-0005](../work/0005-save-lifecycle-compatibility-contracts.md)에서 MOD-008..019의
12개 reference 계약으로 고정했고,
[GDJ-0006](../work/0006-save-lifecycle-product-slice.md)은 typed Save option/field mask,
explicit key와 SQLite error 경계를 구현해 12개 모두 통과했습니다. 그 시점에는 public
migration file, autodetector, graph, lock과 crash recovery가 이후 별도 work/ADR 범위였습니다.

M1부터 열린 Q-007을 먼저 닫기 위해
[GDJ-0007](../work/0007-queryset-evaluation-cache-compatibility-contracts.md)은 QuerySet
evaluation/cache의 QRY-011..021 exact reference 계약을 네 번째 set으로 고정했습니다.
당시 기존 제품 34개는 계속 `passing`이었고 새 11개는 `oracle_locked`였습니다.
[GDJ-0008](../work/0008-queryset-evaluation-cache-product-slice.md)은 Go-native value-copy/
concurrency ownership과 terminal API를 ADR-0012로 결정하고 실제 adapter를 연결해
QRY-011..021을 모두 `passing`으로 올렸습니다. GDJ-0008 완료 당시 검증된 manifest contract는 총
45개이며, 이는 M2 migration이나 M4 QuerySet breadth 전체 완료를 뜻하지 않습니다.

[GDJ-0009](../work/0009-migration-planning-compatibility-contracts.md)은 기존
MIG-001..004 executor/editor 제품 경계를 바꾸지 않고 MIG-005..016으로 dependency graph,
applied state, multi-target forward/backward plan과 잘못된 graph/history의 외부 의미를
다섯 번째 exact set에 고정했습니다. 기존 45개 제품 contract는 계속 `passing`이고 새
12개는 `oracle_locked`이므로 총 57개를 제품 통과로 표현하지 않습니다.

[GDJ-0010](../work/0010-immutable-migration-planner-product-slice.md)은
[ADR-0013](adr/0013-immutable-migration-planner.md)의 immutable identity graph와 별도
AppliedState를 backend-neutral zero-I/O Planner로 구현하고 fifth-set actual adapter를
연결했습니다. MIG-005..016도 `passing`이 되어 GDJ-0010 완료 당시 다섯 제품 set의 검증 범위는 총
57개였습니다. 이 planning adapter의 zero-I/O는 실제 DB probe가 아니라 pure structural
경계로 검증합니다.

[GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md)은
MIG-017..026으로 여러 migration의 migration별 transaction, 중간 실패의 durable/rollback
경계, 이후 단계 중단, ProjectState progression, mixed preflight와 empty no-op를 여섯 번째
exact set에 고정했습니다. 총 reference contract는 67개지만 새 10개는
`oracle_locked`이고 제품 `passing`은 기존 57개뿐입니다.

완료된 [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md)는 최소
`ExecutePlan`과 full zero-I/O preflight, migration별 기존 Apply/Unapply 실행, first-failure
last durable state를 구현했습니다. Django backward의 `schema_then_record`와 달리 schema와
recorder를 같은 transaction으로 유지하는 결정은
[ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)와
[DEV-0001](DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
Accepted/Verified 상태입니다. GDJ-0012 완료 당시 제품 분류는
`63 passing + 4 deviation`이었습니다.

완료된 [GDJ-0013](../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)은
recorder table 없음/empty/record/unrecord, fresh executor의 applied-prefix tail plan,
unknown legacy row와 explicit inconsistent-history preflight, 중간 실패 뒤 재계획을
MIG-027..036의 일곱 번째 exact set으로 고정했습니다. Reference 총계는 77개지만 새 10개는
`oracle_locked`이고 GDJ-0013 완료 당시 제품 상태는 계속
`63 passing + 4 deviation`이었습니다.

완료된 [GDJ-0014](../work/0014-recorder-backed-restart-planning-product-slice.md)는 Accepted
[ADR-0015](adr/0015-recorder-backed-applied-state.md)의 별도 raw read port,
`LoadAppliedState`, explicit `Planner.CheckHistory`와 SQLite read-only reader를 구현했습니다.
Fresh file-backed restart를 포함한 MIG-027..036이 10 `passing`으로 전환되어 GDJ-0014
완료 당시 제품 분류는 `73 passing + 4 deviation`이었습니다. Read/check/plan은
`ExecutePlan`과 한 API가 아니며
snapshot과 실행 사이 lock을 보장하지 않습니다.

완료된
[GDJ-0015](../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)는
loaded migration definition으로 explicit empty, first/middle before·after, cross-app
dependency, multiple target/shared dependency, omitted-target latest leaves와 applied-prefix/
unrelated-known startup `ProjectState`, unknown legacy identity의 schema-state 제외 의미를
MIG-037..046의 여덟 번째 exact set으로 고정했습니다. 새 10개는 `oracle_locked`이고
기존 일곱 product set은 `73 passing + 4 deviation`이므로, reference 87개 전체를 제품
통과로 표현하지 않습니다. 여덟 set의 contract/scenario는 전역으로 유일하고 56개 ordered
cross-binding이 거부됩니다.

완료된
[GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)과 Accepted
[ADR-0016](adr/0016-historical-project-state-reconstruction.md)은 loaded-definition replay를
별도 immutable reconstructor로 구현했습니다. Explicit empty/latest/before/after/applied tagged
request, Planner graph kernel 공유, definition/operation deep-copy와 structured error를 검증했고
MIG-037..046은 10 `passing`, GDJ-0016 완료 당시 제품 분류는
`83 passing + 4 deviation`입니다. Public
migration file/source loader, CLI, data callback과 lifecycle lock/crash recovery는 계속 후속
범위입니다.

완료된
[GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
source loader/CLI보다 lifecycle 경계를 먼저 계약화합니다. Fresh/applied-prefix/no-op에서
latest, named forward/reverse, app zero target, unknown legacy, inconsistent known history와
중간 실패/restart를 MIG-047..056의 아홉 번째 exact set으로 잠갔습니다. 제품 source와 GoDj
adapter를 만들지 않았으므로 GDJ-0017 완료 당시 결과는 기존 `83 passing + 4 deviation`과 새
`10 oracle_locked`를 구분한 9 set/97 contract였습니다. 72개 ordered cross-binding은 모두
validation에서 거부됐습니다.

별도 [Accepted ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)은 recorder identities와
opaque freshness revision을 같은 snapshot으로 읽고 expected token을 각 migration transaction의
첫 DDL/write 전에 검증합니다. `conformance/lifecyclefence/**` spike는 persistent epoch와
monotonic revision 후보로 stale-before-write, step 사이 경쟁, simultaneous contender, fault
rollback과 resource release를 입증했지만 제품 storage/encoding을 고정하지 않았습니다. Outer
transaction과 automatic retry 없이 ADR-0014의 migration별 durable commit을 보존합니다.
GDJ-0017 시점에는 제품 source/API가 없었으며 loader/operation codec, project-binary/CLI, data
callback, exclusive cutover와 crash repair/lease는 후속 범위로 남았습니다.

완료된 [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 already-loaded
`[]Migration`과 실제 `Executor.Backend`를 사용하는 tagged latest/targeted
`Executor.Migrate`를 제품화했습니다. Existing port를 widen하지 않는 backend-owned opaque
revision session은 exact-one snapshot, mandatory Close와 call 사이 connection-free lifetime을
가지며, dedicated fenced transaction은 rolled-back/committed/unknown durability를 구분합니다.
SQLite metadata v1의 epoch/revision/fingerprint와 pinned `BEGIN IMMEDIATE`가 schema, recorder와
successor revision을 한 transaction에 결속합니다. Unsupported fallback과 existing-recorder
자동 adoption은 없고, rolled-back/unknown 뒤 SQLite session은 poison/no-retry입니다.

Accepted ADR-0013의 canonical ascending planner policy도 유지했습니다. Lifecycle 9개는 exact
`passing`, MIG-052의 `result.plan[0..2]`/`metrics.steps[0..2]` 여섯 path만 DEV-0002 sparse
expectation으로 검증해 GDJ-0018 완료 당시 9 product set은
`92 passing + 5 deviation`이었습니다. 기존 DEV-0001과 locked lifecycle
oracle/static/SHA256SUMS, completed `conformance/lifecyclefence/**`는 변경하지 않았습니다.
Default-bearing SQLite `AddField`는 empty table에서 logical default를 보존하고 physical
persistent default 없이 적용하며 nonempty table은 계속 unsupported입니다.

[완료된 GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)은 explicit
caller-provided definition document, strict data-only JSON v1, compatibility tuple `(1,1,1,2)`,
fully normalized Schema IR v2, closed `CreateModel`/non-PK `char`·`boolean` `AddField` codec와 atomic
deterministic load를
MIG-057..064로 잠갔습니다. [Accepted ADR-0019](adr/0019-versioned-migration-definition-source.md)는
Django의 Python migration file ABI를 복제하지 않고 named identity/dependency/ordered operation
의미를 Go data format으로 재설계합니다.

GDJ-0019는 당시 기존 9 product adapter의 `92 passing + 5 deviation`을 그대로 보존하면서
8 `oracle_locked`를 더해 10 reference set/105 unique contract와 90 ordered cross-binding
rejection을 검증했습니다. Exact tuple, strict JSON, canonical digest, atomic loader snapshot과
failure precedence는 ADR-0019 decision provenance이고 Django에서 실제 관찰한 공통 동작은
MIG-057/MIG-064의 별도 provenance로 구분합니다.

`conformance/definitionload/**`의 test-only proof는 실제 `migrations.NewPlanner`와
`Executor.Migrate` handoff를 실행하지만 제품 source나 importable package가 아닙니다. Product
loader와 열 번째 GoDj adapter는 GDJ-0019 당시 구현하지 않았습니다. MIG-064도 당시에는 existing
`Executor.Migrate`에 대한 oracle reference handoff shape이지 Go product loader 지원이
아니었습니다.

[완료된 GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)은 baseline
`eecc75f7507414ad6043a090c97b84080ab0fb8b`에서 activation되어 Accepted
[ADR-0020](adr/0020-migration-definition-loader-product-shape.md)의 새 leaf package
`migrations/definition`, explicit `Source{SourceID,Document}`와
`Load(...Source) (Set, LoadReport, error)`, zero-value empty set, immutable failure/report와 existing
`Executor.Migrate` handoff를 구현했습니다. Raw source/JSON과 semantic fan-out은 source 2,048,
SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, JSON depth 64, document values 65,536,
batch values 262,144, dependencies 2,047, operations 2,048, `CreateModel` fields 2,048의 exact
10 cap으로 bounded하고 Schema IR wire coordinate는 literal 2 + compile drift gate로 잠급니다.

Loader-owned atomic snapshot은 raw document를 보존하지 않고 accessor/report/error mapping마다
fresh deep copy를 반환합니다. Strict scanner는 closed JSON, any-depth duplicate,
UTF-8/surrogate/numeric lexeme와 canonical RFC 6901 failure order를 bounded lazy path로 처리합니다.
Source-owned 9-code/resource-limit context와 raw `*migrations.PlanningError`/lifecycle error의
ownership을 분리하고, `Set.Migrate`는 existing lifecycle에 exactly-once로 위임합니다.

MIG-057..064의 열 번째 actual adapter는 Django parity가 아닌 decision-reference 8개를
`passing`으로 전환했습니다. GDJ-0020 완료 당시 분류는 exact 10 adapter/105 contract의
`100 passing + 5 deviation`이며 90 ordered cross-binding이었습니다. Status-only manifest 외
기존 reference oracle, static fixture, `SHA256SUMS`와 test-only candidate는 변경/승격하지
않았습니다.

제품 commit `6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1
[run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)의 Ubuntu 24.04와
macOS 15 arm64 job에서 통과했고 Ubuntu는 실제 Linux/386 focused runtime을 검증했습니다.
Completion-documentation commit `a5422f2c1ba5db34986564fc065e4b8e28ef0115`도 별도
[run 31310002784](https://github.com/progresshans/godj/actions/runs/31310002784)의 두 job에서
통과했습니다. EVID-023 append/status 교정 baseline commit
`53729103651bfc34acc5fe07fb4376d5dd78c204`도 별도
[run 31310606332](https://github.com/progresshans/godj/actions/runs/31310606332)의 Ubuntu/macOS 두
job에서 통과했습니다.

GDJ-0020 이후에도 CLI/project orchestration은 별도 결정입니다. Directory/file/module/remote
discovery, global CLI/library/generator semver handshake, writer/upgrade/cache, executable/custom/
data/raw-SQL operation, adoption/repair command, crash recovery와 non-SQLite backend는 이 product
loader slice의 지원 범위가 아닙니다.

완료된 [GDJ-0021](../work/0021-migration-project-check-compatibility-contracts.md)은 다음 제품
구현보다 먼저 가장 작은 project-aware check 경험을 contract-only/test-only로 잠갔습니다. Accepted
[ADR-0021](adr/0021-project-linked-migration-check.md)의 `godj migrations check`는 nearest exact
`godj.toml` 또는 explicit descriptor file을 선택하고, global side가 private project runner를
shell 없이 `-mod=readonly`, `GOWORK=off`, `GOTOOLCHAIN=local`로 build/run하며, linked side가
project-relative flat `*.godj.json` roots를 no-follow로 탐색해 actual `definition.Load`에 정확히 한
번 넘기는 의미를 고정합니다. Public product CLI/API는 이번 work에서 만들지 않았습니다.

MIG-065..074는 Django `migrate --check` parity가 아닌
`decision/ADR-0021/derived=false` independent reference입니다. Marker/descriptor, strict runner
protocol v1, source ordering/symlink safety, build/protocol fault, public exit `0/1/2/3/130`, process
cancel/reap와 11개 parsed/accepted input·catalog·wire/retained-output cap을 검증합니다. 결과는
11 reference set/115 unique contract/110 ordered cross-binding과 새 10 `oracle_locked`입니다. Product는 계속 10 adapter/105
contract의 `100 passing + 5 deviation`이고 새 product adapter나 actual output은 없습니다.

Implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)에서 기존
`ubuntu-24.04` x64 full과 `macos-15` arm64 exact 2개, exact labels `ubuntu-22.04`,
`ubuntu-24.04-arm`, `macos-15-intel`, `macos-26`의 project-check 4개와 같은 좌표의 SQLite 4개,
**`2 + 4 + 4 = 10` required job executions**을 모두 통과했습니다. 두 matrix는 Go 1.26.5,
`fail-fast: false`, leg별 20분 timeout, expected GOOS/GOARCH, normal/race/CGO-disabled/vet,
no `continue-on-error`와 final clean worktree를 검증했습니다. Exact 16-file
completion-documentation commit `34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)의 같은 exact 10 job을
모두 통과했습니다. EVID-026 append/status 교정 commit
`f7fbbd50465a610ed9492227909eece524455f15`도 별도 run `31322959993`의 같은 exact 10 job을
통과했습니다.

Windows는 native process/path contract 전에는 지원 runner를 만들지 않습니다. Current actual backend는
SQLite뿐이므로 PostgreSQL/MySQL service-only job도 false green으로 금지합니다. Future backend job은
digest-pinned service image, health check, UTC timezone과 C locale 또는 명시적으로 승인된 collation,
actual query/write/transaction/schema/migration/recorder/revision-lifecycle 및 durable restart/
persistence contract를 먼저 실행해야 합니다. Expected contract 수와 executed 수가 같고 `skipped=0`,
`continue-on-error` 없음, final clean worktree도 필수이며 adjacent versions는 별도 non-required
scheduled matrix로 확장합니다.

GDJ-0021에서 “DB-free”는 GoDj-owned DB/recorder/lifecycle call 0만 뜻합니다. Linked project binary의
임의 user package `init()` side effect까지 차단한다고 주장하지 않습니다. Recursive/module/embed/
remote discovery, Windows, persistent runner cache, full CLI/library/generator handshake, direct
project command dispatcher, writer/upgrade와 DB-aware migration execution은 GDJ-0022 뒤에도
남깁니다. GDJ-0021의 Accepted/Verified 상태는 reference-only/test-only contract에 한정하며 제품
명령 구현으로 승격하지 않습니다.

Completed [GDJ-0022](../work/0022-migration-project-check-product-slice.md)와 Accepted
[ADR-0022](adr/0022-project-runtime-and-global-migration-check.md)는 그 다음 product 단면을 구현했습니다.
Exact 두 global argv, public `project.Config`/`project.Run`, independent internal global/linked/protocol
kernel과 flat discovery가 MIG-065..074를 actual product adapter에서 10 `passing`으로 전환했습니다.
Test-only proof는 byte-preserved independent gate로 남고 product code가 import하지 않습니다. 현재
문단의 GDJ-0022 완료 시점 분류는 11 adapters/115 contracts=`110 passing + 5 deviation`이었습니다.
Completed GDJ-0025의 REL-001 metadata와 REL-004 required predicate actual까지 포함한 현재 분류는
상단의 12/127=`112 passing + 5 deviation + 10 oracle_locked`입니다.

Hosted gate는 existing full/exact 2 + test-only proof 4 + actual SQLite 4를 보존하고 Linux/macOS
x64/arm64 actual product CLI 4와 exact Python 3.12.13/3.13.15/3.14.3/3.14.7 compatibility 4를
별도 추가한 exact 18 required executions입니다. Portable/compatibility는 uv 0.12.3, embedded profile을
재현하는 historical exact darwin oracle만 uv 0.10.12를 사용합니다. Initial fix-head run `31329294154`의
exact 18/18과 앞선 four-Python pre-test assertion failure/cancel은 EVID-028에 보존했습니다.
EVID-028/status head run `31330601427`은 16 success/2 macOS product normal failure였고, final process
synchronization head `385382efffd1872ae7fb427192bab27b95dc57e2`의 run `31332208055`는 exact 18/18
성공했습니다. Failure/fix/job/checkout 증거는 EVID-029에 기록했고, EVID-029/status commit
`1f161f311daa775e6a386ec0df568ff85d681f15`도 run `31333420261` exact 18/18을 통과해 EVID-030에
기록했습니다. GDJ-0023 implementation head `b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`도
[run 31338151743](https://github.com/progresshans/godj/actions/runs/31338151743)의 exact 22/22를
통과했습니다. Completion-documentation head `31784ae1e8261ad0698921b93803aa35e9b63f93`도 별도
[run 31339409336](https://github.com/progresshans/godj/actions/runs/31339409336)의 exact 22/22와
[EVID-20260810-033](status/TEST_EVIDENCE.md#evid-20260810-033--gdj-0023-github-hosted-completion-documentation-head-exact-22-job-ci)으로
검증했습니다. Final evidence/status head `50578ddc4756452b2a9a0d2afd75711a35b76d8a`도
[run 31340170361](https://github.com/progresshans/godj/actions/runs/31340170361)의 exact 22/22와 273/273
steps를 성공해 EVID-034에 기록했습니다. 그 뒤 GDJ-0024 activation docs 자체의 commit/push와 exact-head
CI는 activation run `31344980929` exact 22/22로 해소됐습니다. GDJ-0024 implementation head
`05e6e218db16e17ce13f7b504a01c603041e4a2a`도
[run 31348285559](https://github.com/progresshans/godj/actions/runs/31348285559)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-036에 기록했습니다. Completion-documentation head
`e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`도 별도
[run 31349791188](https://github.com/progresshans/godj/actions/runs/31349791188)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-037에 기록했습니다. Final evidence/status head
`5bf143575e9b703117a328c1fc5b7eb5823fbfd6`도 run `31351169780`의 exact 26/26 jobs·326/326 steps를
성공해 EVID-038에 기록했습니다. 이 clean tested head가 GDJ-0025 activation baseline이었고 activation
commit `cf8cb589...`도 run `31354040515` exact 26/26을 통과했습니다. GDJ-0025 implementation head
`98db55a30ff71a2f2f70722cb569a046208a5403`은
[run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-040에 기록했습니다. Completion-documentation head
`7b5cebda7410ae8c096a8c30bd60daad1295bbf2`도 별도
[run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)의 exact 26/26 jobs와
326/326 recorded steps를 성공해 EVID-041에 기록했습니다. 이 EVID-041/final-status exact 6-file
patch 자체의 exact-head CI는 후속 검증이며 run `31358640776`을 재사용하지 않습니다.
이는 PostgreSQL/MySQL
service-only job 추가가 아닙니다. M3의 첫 PostgreSQL required job은 Q-013/actual backend contract와
query/write/transaction/schema/migration/recorder/revision lifecycle 및 durable persistence 구현 뒤에만
추가합니다. MySQL은 M9 actual adapter까지 같은 원칙을 따릅니다.

## M3 — Relations + PostgreSQL

- Completed [GDJ-0023](../work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md)은
  ForeignKey 외부 동작 REL-001..012와 Q-013 cross-app binding/import-cycle/shared-AST feasibility를
  contract-only/test-only로 고정했습니다. Schema IR v2나 제품 API는 변경하지 않았습니다.
- [ADR-0023](adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)은 symbolic/atomic binding,
  import-cycle-free project bridge, shared immutable relation AST와 explicit vNext field-union relation arm
  방향을 Accepted했습니다.
- Completed [GDJ-0024](../work/0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md)와
  Accepted [ADR-0024](adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)는 exact
  `RelationFormatVersion=3` ForeignKey arm/DSL, mixed v2 target/v3 source additive companion, atomic
  `orm.BindProject`와 REL-001 metadata-only product subset을 동결합니다. REL-002..012는 oracle-locked로
  유지하며 GDJ-0024 completion aggregate는 product
  `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`, relation 1/12입니다. Existing exact 22에
  relation-product 4 legs를 더한 exact 26은 implementation run `31348285559`와 별도
  completion-documentation run `31349791188`에서 모두 통과했습니다.
  OneToOne/query/eager/write/delete/DDL/migration codec와 PostgreSQL actual backend는 뒤의
  bounded pair로 계속 분리합니다.
- Completed [GDJ-0025](../work/0025-forward-foreign-key-predicate-product-slice.md)와 Accepted
  [ADR-0025](adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)는 required AutoField-target
  `author__name`/`author__id` exact predicate를 additive query companion, project-bound shared relation path와
  SQLite reusable INNER JOIN으로 연결했습니다. REL-004만 `passing`으로 전환해 completed aggregate는
  `112 passing + 5 deviation + 10 oracle_locked`, relation REL-001/004 2/12입니다. Loader/cache, nullable
  `isnull`, reverse/eager/write/delete/DDL/migration과 PostgreSQL은 명시적 비목표입니다. Implementation run
  `31357283530`과 completion-documentation run `31358640776`은 모두 exact 26/26·326/326을 통과했습니다.
- ForeignKey, OneToOne, reverse relation
- cascade와 database-level delete 선택
- `select_related`, `prefetch_related`
- 앱 간 관계/import 전략 검증
- SQLite와 PostgreSQL conformance

## M4 — QuerySet Breadth

- Q/F expression, aggregate, annotation
- projection, subquery, window function
- bulk operation, locking, custom lookup/field extension
- result cache와 iterator semantics 확정

Q-007의 result cache/terminal semantics는 이후 projection/aggregate/relation loader가
같은 평가 상태를 재사용하기 전에 GDJ-0007/0008의 선행 단면으로 완료했습니다. 이는
M4 전체 breadth가 구현됐다는 뜻이 아닙니다.

## M5 — Web Core

- settings, app registry, system check
- routing/reverse, middleware, request/response, error handling
- view와 template 한 요청 수직 단면
- development server와 management command

## M6 — Forms, Auth, Admin

- common validation core와 Form/Serializer 경계
- Form, ModelForm, CSRF, session, auth, permission
- 한 모델의 Admin list/search/edit/history/action 수직 단면
- static/messages와 접근성·보안 gate

## M7 — API

- API reference profile 확정
- serializer, parser/renderer, authentication/permission
- APIView/ViewSet/Router, pagination/filter/order
- OpenAPI와 browsable API

## M8 — Realtime

- Realtime reference profile 확정
- WebSocket/SSE consumer와 protocol router
- auth/session middleware, group, channel layer
- in-memory와 Redis backend, backpressure/lifecycle

## M9 — Backend Expansion

- MySQL, MariaDB, Oracle
- multi-DB와 database router
- capability-driven conformance와 explicit unsupported paths

## M10 — Advanced + 1.0

- GIS, i18n, FormSet, advanced Admin, contrib
- security audit, performance baseline, migration stability
- compatibility matrix와 Django DB migration tools
- generated code/schema/migration upgrade policy
- API freeze, tutorial, release engineering

## 작업 분할 원칙

- 한 work item은 사용자에게 보이는 하나의 결과와 실행 가능한 완료 조건을 가집니다.
- 한 단계에서 모든 Field/API를 만들지 않고 다음 수직 단면에 필요한 최소 폭만 구현합니다.
- 조사 spike와 production implementation을 구분합니다.
- 관계 없는 package나 같은 공개 API를 병렬 에이전트에 나누지 않습니다.
- 긴 milestone은 contract group별 work item으로 쪼개되 milestone gate는 하나의 통합 담당자가 닫습니다.
