# 동시성·비동기·성능 방향

- 상태: 기본 방향 Accepted; QuerySet 평가 상태와 bounded relation-object cache 계약 Accepted
- 마지막 검토: 2026-08-12

GoDj의 출발점에는 Django보다 적은 프로세스와 메모리 중복으로 I/O 동시성과 멀티코어 실행을 자연스럽게 활용하려는 목적이 있습니다. 그러나 “Go로 작성했다”는 사실만으로 성능을 주장하지 않습니다. 측정 가능한 workload와 회귀 기준이 있어야 합니다.

## 채택한 기본 방향

- HTTP 요청 처리는 Go server와 goroutine 모델을 사용합니다.
- Django의 sync/async 쌍을 기계적으로 복제하지 않습니다.
- DB, network, filesystem I/O API는 `context.Context`로 취소와 deadline을 전파합니다.
- 호출자가 goroutine concurrency를 조정하며, ORM이 호출자 몰래 무제한 background goroutine을 만들지 않습니다.
- request-scoped 값은 전역 mutable state가 아니라 명시적 context 또는 request container로 전달합니다.
- connection pool은 공유할 수 있지만 transaction은 획득한 connection과 수명주기가 결합됩니다.
- 지원 타입의 goroutine 안전 여부를 공개 문서와 race test로 명시합니다.
- streaming, WebSocket, SSE, file upload에는 backpressure와 resource limit 계약이 필요합니다.

## QuerySet과 실행 상태

Query plan은 불변이고 결과 cache는 plan과 분리합니다. 이 ownership과 terminal 의미는
[ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)와 completed GDJ-0008에서 Accepted/implemented됐습니다.

- Direct `QuerySet` value copy만 같은 evaluation-state pointer를 공유합니다.
- `Filter`, `OrderBy`, 성공한 `Limit`와 `Fresh`는 새 unpopulated state를 받습니다.
- 같은 state의 동시 `All`은 한 owner flight로 합치고 waiter cancellation은 owner를 취소하지 않습니다.
- 성공한 empty/non-empty full result만 cache하며 backend/scan/rows/close/context failure는 재시도 가능합니다.
- Canonical cache는 descriptor clone 뒤 저장하고 모든 caller 반환도 clone해 pointer alias를 노출하지 않습니다.
- Cold `Count`/`Exists`/`At`/`First`는 full cache를 채우지 않고 warm terminal은 이를 재사용합니다.
  `Iterate`는 cache를 읽거나 바꾸지 않습니다.
- 모든 terminal은 nil/already-canceled context를 warm cache보다 먼저 거부합니다.

Completed GDJ-0026/[Accepted ADR-0026](adr/0026-forward-foreign-key-object-cache-and-nullability.md)은 이 existing
QuerySet state machine을 required/nullable forward object handle 내부의 target-PK `Limit(2).All`에 재사용하는
bounded relation-object ownership을 채택했습니다. Generated opaque pointer wrapper 하나가
relation별 QuerySet state를 소유하고, direct pointer alias만 같은 cache를 공유하며 `Fresh`와 별도 `From`은
독립 state를 받는 것입니다. 성공적으로 materialize한 0/1/2-row snapshot은 QuerySet cache에 저장합니다.
0-row와 2-row는 warm access에서도 각각 `related_object_missing`/`related_object_cardinality`로 다시 분류하며
재실행하지 않고, backend/query/scan/rows/close/context failure만 cache하지 않아 다음 독립 호출이 재시도할
수 있습니다. Nullable local NULL은 DB I/O 없는 absent success이며 nil/already-canceled context 검사는 이
null/warm fast path보다 먼저 실행합니다. Exact implementation head `5be46141...`은 run `31370313755`의
26/26 jobs·326/326 recorded steps와 independent hosted audit P0/P1/P2/P3=0을 통과했습니다. 이 동시성
계약은 bounded forward object slice만 Accepted하며 eager priming, write invalidation과 multi-backend lifetime은
여전히 결정하지 않습니다.

Completed GDJ-0027/Accepted ADR-0027은 reverse accessor의 ownership을 별도 pointer/self-sentinel owner wrapper와
`RelatedSet`으로 채택했습니다. `RelatedSet.All`은 existing QuerySet success-cache/singleflight/clone/cancellation/
retry를 재사용하고 `OrderBy`/`Fresh`는 새 pointer와 evaluation state를 만들며 I/O를 하지 않습니다. Public
Filter/IN/prefetch/warm injection은 추가하지 않고 REL-012용 speculative state도 만들지 않습니다. 이 계약은
exact implementation head `7db68415...`의 run `31419940399`에서 26/26 hosted gate를 통과했습니다. Acceptance는
bounded one-hop related-set slice뿐이며 eager priming, prefetch warm publication, write invalidation과
multi-backend lifetime은 여전히 결정하지 않습니다.

Completed GDJ-0028/Accepted ADR-0028은 REL-012에 한해 private warm publication을 명시적으로 채택했습니다.
`ReversePrefetch` handle은 immutable/copyable하고 `Load` 호출별 batch evaluation state는 독립이며 내부
goroutine이나 cross-call singleflight를 만들지 않습니다. Owner/row clone, distinct-key sort/dedupe, one batch
evaluation과 모든 grouping validation이 성공한 뒤에만 각 owner 위치마다 독립 ready evaluation state를 가진
`RelatedSet`을 publish합니다. 실패/cancellation에서는 output nil, partial cache 0이고 rows는 exactly once
close되며 다음 독립 `Load`는 재시도할 수 있습니다. Warm set의 concurrent `All`은 existing evaluation mutex와
clone contract를 사용해 I/O 0이고 nil/already-canceled context가 cache보다 먼저 이깁니다. `Fresh`/`OrderBy`는
new cold state이며 retained backend/session이 끝났다면 해당 backend 오류를 반환합니다. Exact implementation
head `4858ab88...`의 run `31432551159`는 26/26 hosted gate와 independent audit P0..P3=0을 통과했습니다.
Acceptance는 bounded per-call batch/ready-set ownership뿐이며 transaction/session의 goroutine 공유, background
batching, chunking, public cache injection, eager priming과 write invalidation은 여전히 결정·지원하지 않습니다.

Completed GDJ-0029/Accepted ADR-0029는 eager evaluation을 existing QuerySet cache에 섞지 않고 별도
`ForwardSelectQuery` state가 소유하도록 동결했습니다. 호출별로 fresh source/target projection scans를 만들고
joined row 전체, `Rows.Err`, close 결과와 context를 검증한 뒤에만 source clone과 ready `RelatedObject` pair를
한 번에 publish합니다. 실패/cancellation은 nil output, partial ready state 0, acquired rows exactly-once close이며
independent retry가 가능합니다. Successful cached `All`도 nil/already-canceled context보다 먼저 이기지 않고,
return/copy마다 clone과 pointer state를 분리합니다. Exact implementation head `c02aab67...`의 EVID-056/run
`31470292759`는 26/26·326/326 hosted gate, race/CGO-disabled/vet와 four-coordinate 630/630/0 inventory로 이
bounded semantics를 검증했습니다. Cross-call singleflight, public cache injection, transaction/session goroutine
sharing, write invalidation, multiple/reverse eager graph는 계속 비목표입니다.

Completed GDJ-0030/Accepted ADR-0030은 relation delete에 existing deferred `db.Atomic`을 재사용하지 않습니다.
SQLite backend의 additive `db.RelationAtomic.AtomicRelation`은 pinned relation connection에서
`PRAGMA foreign_keys=1`을 확인한 뒤 raw `BEGIN IMMEDIATE`를 실행하고 complete PROTECT scan, SET_NULL UPDATE와 target
DELETE/COMMIT을 같은 connection에 묶습니다. `database/sql`이 raw transaction을 추적하지 않으므로 session은
terminal SQL 전에 inactive가 되고, active transaction 가능성이 있는 physical connection은 pool로 반환하지
않습니다. Test가 소유한 모든 competing writer connection도 FK-on을 각각
확인해야 합니다. File-backed two-connection gate는 no-wait writer의 BUSY/no retry와 wait-through-COMMIT writer의
FK rejection/no orphan을 따로 증명합니다. SQLite FK enforcement는 per-connection이므로 `Open`이 자동 강제하거나
FK-off/out-of-band writer를 framework가 막는다고 주장하지 않습니다. Wait-through-COMMIT safety는 모든 declared
incoming edge의 metadata-matching physical `NO ACTION`/`RESTRICT` FK가 존재해야 하며 fixture
`PRAGMA foreign_key_list`가 증명하는 supported schema precondition입니다. Missing/mismatched constraint는
unsupported이고 relation DDL/runtime repair를 주장하지 않습니다.

Callback error/context/statement/unexpected-count pre-COMMIT failure와 COMMIT-call error는 canceled callback context가
아닌 bounded cleanup context로 raw ROLLBACK을 시도합니다. Rollback 실패나 불확실한 autocommit은 `Conn.Raw`에서
`driver.ErrBadConn`을 반환하는 방식 등으로 physical discard합니다. Confirmed rollback만 normal Close하고 confirmed
discard/done은 release합니다. Discard 확인도 실패하면 Close하지 않고 poisoned connection을 retain해 pool reuse를
막으며 resource loss와 cleanup error를 감수합니다. Relation cleanup helper는 explicit termination-confirmed bool을
반환하고 `driver.ErrBadConn`/`sql.ErrConnDone`만 confirmed discard/done으로 인정합니다. `Conn.Raw` nil은 confirmation이
아니며 기존 migration helper semantics를 그대로 재사용하지 않습니다. Private per-Backend retention state가 해당
physical handle을 강하게 보유하므로 later borrower가 그 raw transaction을 상속할 수 없습니다. 다만 retained lock은
명시적 Backend close 전까지 다른 connection을 BUSY/block 상태로 둘 수 있으며, 이를 lock-free reborrow로 과장하지
않습니다.
Returned-error path는 primary error와 cleanup error를 모두 보존합니다. Callback panic은 session을
inactive로 만든 뒤 detached rollback/discard하고 exact original panic value를 바꾸지 않고 re-panic합니다. 이때
cleanup error는 best-effort/non-returnable이지만 confirmed discard 또는 poisoned-handle retention으로 transaction
inheritance를 막아야 합니다. Panic을 recover한 caller는
marker로 cleanup 결과를 구분할 수 없으므로 재호출 전 external reconciliation이 필요합니다. Pre-COMMIT confirmed rollback/discard만 unchanged DB를
보장합니다. SQLite relation session은 모든 `Insert`/`Update`/`Delete`/`RelationSetNull` 호출 직전에
mutation-possible을 표시하며 이 deleter의 첫 entry는 SET_NULL/target DELETE입니다. 그 뒤 rollback과
forced-discard confirmation이 모두 실패한 path는 unchanged pointer,
stable `backend_error/transaction_outcome_unknown`, DB outcome unknown/reconciliation-required입니다. Primary와 cleanup
cause는 outermost marker의 joined Cause에 모두 남고 이 code는 COMMIT 호출을 뜻하지 않습니다. Raw BEGIN/callback-0,
pre-mutation read/PROTECT/resource failure, confirmed cleanup,
panic rethrow와 literal COMMIT error에는 이 code를 쓰지 않으며 literal COMMIT은 별도 `commit_outcome_unknown`입니다.

Raw `BEGIN IMMEDIATE` error(BUSY 포함)는 no-transaction proof가 아니므로 callback/retry 없이 pinned connection을
`Conn.Raw`→`driver.ErrBadConn`으로 force-discard하고 primary+discard error를 검증합니다. Confirmed discard는 clean
reborrow를, unconfirmed discard는 poisoned physical handle의 비재사용/비상속을 검증합니다.
Confirmed discard/done일 때만 release/Close할 수 있습니다. Discard 확인도 실패하면 Close 호출 0으로
poisoned/unreleased connection을 retain합니다. Callback/mutation 0이므로 이 path는
`transaction_outcome_unknown`/`commit_outcome_unknown` 둘 다 아닙니다.

Retention state는 process-global registry가 아니라 `Open`이 초기화하는 private Backend-owned pointer입니다.
`Backend.Close`는 existing CAS 뒤 `sql.DB.Close`를 먼저 호출해 pool을 봉인하고, 그 결과가 error여도 retained set을
seal/take/drain한 뒤 DB-close와 `database/sql`이 실제 반환한 retained-handle close error를 join합니다. Drain의
`Conn.Close`는 terminal driver-close path를 시도하며 no-repool을 보장하지만, `database/sql`이
underlying driver-close error를 `Conn.Close`로 노출하지 않으므로 그 성공/오류를 과장하지 않습니다. Close와 retain의 경합은 state mutex/closed latch로 선형화합니다. Seal 전 retain은 drain되고,
seal 뒤 retain은 즉시 같은 terminal close path로 들어가며, 두 번째 Backend close는 아무것도 다시 닫지 않습니다.
이는 순차 idempotence 계약이며 existing CAS에서 진 concurrent losing Close가 winner의 drain 완료를 기다린다고
주장하지 않습니다.

Literal COMMIT call이 error를 반환한 경우만 stable `backend_error/commit_outcome_unknown`, durability unknown,
`(0,error)`/unchanged pointer입니다. 두 outcome-unknown marker 모두 GoDj 내부 자동 재시도는 0이고 caller는
cleanup 결과와 무관하게 external reconciliation 전 명시적으로 재호출해서는 안 됩니다. 이 packet에는 재호출을 탐지·거부하는 poison token/fence/registry가
없으므로 그 caller 의무를 runtime-enforced gate라고 주장하지 않습니다. Successful COMMIT은 authoritative하여 later context/connection-close error가 success `(1,nil)`을
downgrade하지 않습니다. Session은 callback 밖에서 invalid이고 goroutine 공유를 약속하지 않습니다. 이
canceled-context rollback/forced-discard/reborrow/fault/race/CGO0 gates를 모두 통과한 bounded semantics입니다.

`AtomicRelation` callback cardinality는 precondition/begin failure 0회, 그 밖에는 synchronous exact 1회입니다.
Concurrent/retry/after-return invocation은 port violation입니다. ORM은 `AtomicRelation` 반환 직후 guard를 원자적으로
seal하고 completed callback count/result를 snapshot합니다. Nil-without-callback과 seal 전에 등록된
second/concurrent entry는 `backend_error/invalid_plan`, outer `(0,error)`/unchanged key와 rejected-entry mutation 0을
만듭니다. Seal 이후 entry는 그 호출 자체만 `backend_error/invalid_plan`/mutation 0으로 거부하며 이미 결정된 outer
결과나 caller key를 소급 변경하지 않습니다. Interface에는 backend-return hook이 없으므로 악성 backend의 first
callback이 `AtomicRelation` return과 seal 사이를 경합해 seal 전에 완료되는 경우는 synchronous callback과 구분할 수
없는 port violation이고 탐지/outer-result를 보장하지 않습니다. Caller key clear는 sealed snapshot이 exactly one
completed successful callback임을 확인한 뒤에만 가능합니다. Backend가 callback error를 삼키거나 commit하면 DB
outcome은 보장하지 않습니다.

이 bounded transaction/concurrency semantics는 implementation head `c3803acb...`의 EVID-061/run
`31510689383`에서 exact 26/26·326/326 hosted gate, normal/race/CGO-disabled/vet/actual Linux-386,
file-backed competing-writer와 retained-handle lifecycle fault gates를 통과했습니다. Successful COMMIT 뒤
context transition은 `(1,nil)`과 caller key clear를 보존하고, outcome-unknown marker는 external reconciliation
의무만 나타내며 runtime poison/fence를 추가하지 않습니다. Canonical facade/cache invalidation과 non-SQLite
transaction semantics는 계속 open입니다.

Completed GDJ-0033/Accepted ADR-0033은 forward assignment/save의 wrapper ownership을 별도로 고정했습니다.
`With*`/clear는 original source를 바꾸지 않는 fresh wrapper를 반환하고 changed relation edge에만 새 mutable cache
cell을 배정합니다. Unrelated ready/absent snapshot은 독립 cell로 복제하고 cold/flight state는 공유하지 않습니다.
Target wrapper `Save`와 같은 wrapper 접근을 동시에 수행할 때는 caller synchronization이 필요하며 global identity
map이나 cross-materialization pointer identity는 없습니다. Save의 corrected canonical three-phase preflight는 모든
cache tuple, 모든 assigned-target origin과 edge별 단 한 번의 PK snapshot, 첫 no-PK target을 순서대로 검증합니다.
모든 candidate raw/write/object/cache가 성공하기 전에는 source state를 publish하지 않고 backend I/O도 시작하지
않습니다. Transaction rollback은 target/source wrapper memory를 자동 rewind하지 않으며 session-origin wrapper는
callback 내부만 supported이고 callback 이후 warm/cold 동작은 noncontractual입니다. Exact implementation head
`be6f3d4e...`의 EVID-076/run `31586910749`은 normal/race/CGO-disabled/vet와 exact 26/26 hosted jobs·326/326 steps를
통과했습니다. 이 검증은 reverse/general facade, callback-after-return lifetime enforcement, typed generated
`select_related` cause-loss P2, relation-capable migration 또는 non-SQLite concurrency semantics를 주장하지 않습니다.

## Transaction과 cancellation

- transaction object를 여러 goroutine이 동시에 사용해도 되는지 기본적으로 가정하지 않습니다.
- context 취소가 query, row iteration, scan, retry 경로 전체로 전달되어야 합니다.
- rollback 실패가 원래 오류를 숨기지 않도록 오류 결합 정책을 정합니다.
- retry는 transaction 의미와 idempotency를 모른 채 자동 적용하지 않습니다.
- connection 반환, row close, statement close, goroutine 종료는 실패 경로에서도 검증합니다.

## Hook, signal, background work

Hook/signal은 실행 순서, sync/async 여부, transaction commit 전후, 오류 전파가 계약되어야 합니다. core가 durable background task를 직접 소유할지는 미결정입니다. process 종료 시 lifecycle과 전달 보장을 정의하지 않은 goroutine을 만들어서는 안 됩니다.

## 성능 검증 원칙

- microbenchmark와 실제 request/DB workload를 구분합니다.
- latency 분포, throughput, allocation, RSS, connection 수, goroutine 수를 함께 기록합니다.
- warm/cold cache, DB/network 조건, CPU 수, Go toolchain을 기록합니다.
- Django 비교는 동일 기능·데이터·DB 의미에서만 수행합니다.
- benchmark 결과는 checkout과 환경이 연결된 `docs/status/TEST_EVIDENCE.md`에 남깁니다.
- 수치 목표와 허용 회귀폭은 M1 walking skeleton이 안정된 뒤 별도 performance ADR에서 정합니다.

## GDJ-0035 Accepted SQLite lifecycle concurrency boundary

GDJ-0035 Phase C는 existing revision-fenced migration transaction을 보존하면서 relation editor/remake를
추가하는 exact concurrency boundary를 test-only proof로 동결했습니다.
[ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)의 bounded design은
EVID-091로 증명된 Proposed docs head 뒤 Accepted됐습니다. D1/D2/D3a bounded product slices는
[EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)에서
구현·검증됐지만 D3b core integration 전에는 아래 전체 lifecycle을 지원하는 보장으로 표현하지 않습니다.

- Static request/resource/carrier/profile/digest/graph/chronology/readiness preflight는 backend/session 전에
  끝나며 failure DB/session I/O는 0입니다. 그 뒤 existing fenced session에서 applied history를 exact once
  읽고 actual Planner를 실행한 뒤 actual plan 전체를 dry-validate합니다. Relation capability는 이
  history/plan stage 성공 후 every begin/mutation 전에만 검증하고, SQLite schema/cardinality inspection은
  transaction 안의 별도 physical stage입니다.
- Relation-only/mixed `definition.Set`은 immutable module-private `definitionhandoff.Handoff`를 소유하고
  `Set.Migrate` 호출마다 fresh clone을 nonnil context의 typed unexported key에 붙입니다. Executor는 기존
  context nil/cancel/deadline/value precedence 뒤, capability/session/I/O 전에 carrier를 synchronous하게 읽고
  visible definition clone과 seals를 검증하며 retain하지 않습니다. 같은 Set의 concurrent Migrate는 carrier나
  nested profile/provenance/graph state를 공유 변경하지 않습니다.
- Missing/mismatched carrier는 relation path에서 pre-I/O capability error이고 legacy/empty/raw legacy path는
  zero-carrier behavior를 보존합니다. Context는 handoff transport일 뿐 cancellation/deadline/other values를
  shadow하거나 session/transaction lifetime까지 carrier를 연장하는 storage가 아닙니다.
- `PRAGMA foreign_keys=1`은 relation intent로 고정한 exact physical connection에서 transaction `BEGIN` 전에
  확인합니다. Pool의 다른 connection 상태를 재사용하지 않습니다.
- `BEGIN IMMEDIATE` 뒤 첫 mutation 전에 실행하는 physical preflight는 `sqlite_schema`, `foreign_key_list`,
  index/trigger/view와 inbound FK를 읽는 DB I/O입니다. Pure zero-I/O preflight와 구분하며 실패 시 mutation 0과
  rollback을 요구합니다.
- Physical preflight 뒤 first-write revision/history claim, table remake, row copy, `foreign_key_check`, recorder
  write와 successor revision은 하나의 existing `RevisionFencedTransaction`과 exact connection을 사용합니다.
  Relation용 두 번째 session/transaction은 없습니다.
- Precommit fault는 failed migration을 rollback하고 앞선 migration commit은 보존합니다. Commit outcome은
  success/definite failure/unknown outcome을 구분하고 unknown을 automatic retry하지 않습니다.
- Scalar-only/no-op actual plan은 relation capability/`BeginRelationFencedMigration` call이 0입니다. Nonempty
  scalar-only plan은 existing `BeginFencedMigration`을 사용하며, unsupported relation step이 하나라도 있으면
  scalar prefix를 begin/commit하지 않습니다.
- `BeginFencedMigration`/`BeginRelationFencedMigration`, global PRAGMA/catalog/physical-preflight/claim failure는
  step-level `NoOperation`과 existing typed class를 유지하고, SchemaEditor/row-copy/final-FK failure만
  exact operation을 소유합니다.
- File restart와 concurrent caller alias/race gate는 MIG-078/084/085/086에서 별도로 검증합니다.

Exact order는 `PRAGMA foreign_keys=1` → `BEGIN IMMEDIATE` → physical preflight → fence claim → DDL/remake →
FK check → recorder/revision → `CommitFenced` once입니다. `CommitRolledBack`/`CommitUnknown`은 pre-step state와
token을 보존하고 retry는 0입니다. Candidate-local reopen은 actual epoch/fingerprint/DAG/`StateReconstructor`
restart 증거가 아닙니다. Phase C proof head `7d36502...`/EVID-090은 hosted-verified됐고 Proposed docs-freeze head `5bdf013...`/EVID-091도 별도로
hosted-verified됐고 acceptance docs head `7cdc6d6...`도 EVID-092/run `32187094845`의 고유 exact-head hosted gate를
통과했습니다. D1/D2/D3a는 EVID-093에서 각 bounded slice로 별도 검증됐습니다. D3a는
direct Create/Delete port만 소유하며 Add/Remove/remake caps는 false입니다. Core relation execution은 D3b
전 pre-session Unsupported이고 D3b는 새 public API를 추가하지 않습니다.

Q-019 retained unknown-outcome connection policy는 이 packet이 답하지 않으며, non-SQLite concurrency
semantics도 범위 밖입니다.
