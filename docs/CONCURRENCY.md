# 동시성·비동기·성능 방향

- 상태: 기본 방향 Accepted, 세부 계약 Proposed
- 마지막 검토: 2026-08-07

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

Query plan은 불변이고 값처럼 공유할 수 있는 방향을 채택합니다. 결과 cache는 plan과 별개입니다. 다음은 아직 결정하지 않았습니다.

- 체이닝 시 기존 cache를 폐기하는지 분리하는지
- 같은 QuerySet의 동시 평가를 합치는지 독립 실행하는지
- 성공 결과만 cache하는지 오류도 cache하는지
- `All`, iterator, `Count`, `Exists`, `First`가 cache를 공유하는지
- 평가된 QuerySet을 goroutine 사이에서 공유할 수 있는지

이 질문은 Django의 관찰 가능한 결과 cache 의미, Go race test, allocation/latency benchmark를 함께 보고 ADR로 결정합니다. 초안의 `cache *ResultCache[M]`를 단순 복사하는 설계는 채택된 구현이 아닙니다.

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
