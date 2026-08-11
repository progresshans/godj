# 동시성·비동기·성능 방향

- 상태: 기본 방향 Accepted; QuerySet 평가 상태와 bounded relation-object cache 계약 Accepted
- 마지막 검토: 2026-08-11

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

Active GDJ-0029/Proposed ADR-0029는 eager evaluation을 existing QuerySet cache에 섞지 않고 별도
`ForwardSelectQuery` state가 소유하도록 제안합니다. 호출별로 fresh source/target projection scans를 만들고
joined row 전체, `Rows.Err`, close 결과와 context를 검증한 뒤에만 source clone과 ready `RelatedObject` pair를
한 번에 publish합니다. 실패/cancellation은 nil output, partial ready state 0, acquired rows exactly-once close이며
independent retry가 가능합니다. Successful cached `All`도 nil/already-canceled context보다 먼저 이기지 않고,
return/copy마다 clone과 pointer state를 분리합니다. 이 semantics는 activation target일 뿐 아직 구현·검증되지
않았습니다. Cross-call singleflight, public cache injection, transaction/session goroutine sharing, write
invalidation, multiple/reverse eager graph는 계속 비목표입니다.

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
